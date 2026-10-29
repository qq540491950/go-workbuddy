package agentkit

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"google.golang.org/genai"

	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// ModelFactory builds the model.LLM for a provider/model pair.
func NewModelFromProvider(p config.ModelProvider, modelID string) (model.LLM, error) {
	if modelID == "" {
		return nil, fmt.Errorf("model id is required")
	}
	switch p.Protocol {
	case config.ProtocolAnthropic:
		return NewAnthropic(AnthropicConfig{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: modelID})
	case config.ProtocolGemini:
		return gemini.NewModel(context.Background(), modelID, &genai.ClientConfig{
			APIKey: p.APIKey,
			HTTPOptions: genai.HTTPOptions{
				BaseURL: p.BaseURL, // empty => Google default
			},
		})
	default: // OpenAI-compatible Chat Completions
		return NewOpenAICompat(OpenAICompatConfig{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: modelID})
	}
}

// Kit assembles ADK agent trees from app configuration.
type Kit struct {
	store     *config.Store
	mcp       *mcpmgr.Manager
	memory    tool.Tool       // long-term memory search tool (nil if memory unavailable)
	knowledge *KnowledgeIndex // workspace knowledge vector index (nil => keyword search)
}

// NewKit creates a Kit.
func NewKit(store *config.Store, mcp *mcpmgr.Manager) *Kit {
	return &Kit{store: store, mcp: mcp}
}

// SetMemoryTool attaches the long-term memory search tool.
func (k *Kit) SetMemoryTool(t tool.Tool) { k.memory = t }

// SetKnowledgeIndex attaches the persistent knowledge vector index; combined
// with an embedding provider in settings this upgrades search_knowledge from
// keyword to hybrid semantic retrieval.
func (k *Kit) SetKnowledgeIndex(idx *KnowledgeIndex) { k.knowledge = idx }

// IndexStats exposes the knowledge index state for observability.
func (k *Kit) IndexStats() KnowledgeIndexStats { return k.knowledge.Stats() }

// TestKnowledgeSearch runs a one-off hybrid knowledge search with current
// settings — a tuning aid for the embedding configuration.
func (k *Kit) TestKnowledgeSearch(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("检索词不能为空")
	}
	cfg := k.store.Get()
	opts := k.searchOptions(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts.Ctx = ctx
	if opts.Workspace == "" {
		return "", fmt.Errorf("请先在设置中配置工作区目录")
	}
	return searchHybrid(opts, query)
}

// ResetKnowledgeIndex wipes the vector index; the next semantic search
// rebuilds it from the workspace files.
func (k *Kit) ResetKnowledgeIndex() error { return k.knowledge.Reset() }

// BuildAgent builds an ADK agent for the given agent config.
func (k *Kit) BuildAgent(ac config.AgentConfig) (agent.Agent, error) {
	cfg := k.store.Get()
	return k.buildLLMAgent(ac, cfg)
}

func (k *Kit) buildLLMAgent(ac config.AgentConfig, cfg config.Config) (agent.Agent, error) {
	return k.buildLLMAgentWithSubAgents(ac, cfg, nil)
}

func (k *Kit) buildLLMAgentWithSubAgents(ac config.AgentConfig, cfg config.Config, subAgents []agent.Agent) (agent.Agent, error) {
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == ac.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return nil, fmt.Errorf("agent %q: model provider not found", ac.Name)
	}
	m, err := NewModelFromProvider(*provider, ac.Model)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
	}

	var tools []tool.Tool
	var toolsets []tool.Toolset

	// Built-in tools.
	for _, name := range ac.BuiltinTools {
		switch name {
		case "time":
			t, err := TimeTool()
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		case "files":
			builtin, err := workspaceFileTools(k.workspace(), confirmsTool(ac, "files"))
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, builtin...)
		case "knowledge":
			t, err := SearchKnowledgeTool(k.searchOptions(cfg))
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		case "memory":
			if k.memory != nil {
				tools = append(tools, k.memory)
			}
		}
	}

	// Skills (progressive disclosure via ADK skill toolset).
	if len(ac.SkillIDs) > 0 {
		ts, err := k.skillToolset(ac, cfg)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
		}
		if ts != nil {
			toolsets = append(toolsets, ts)
		}
	}

	// MCP servers. A server whose transport cannot even be built (missing
	// command/URL, OAuth misconfiguration) is reported instead of silently
	// running an agent that is missing tools the user attached — the same
	// contract as the skill toolset above.
	var mcpErrs []string
	for _, id := range ac.MCPServerIDs {
		found := false
		for _, s := range cfg.MCPServers {
			if s.ID == id {
				found = true
				ts, err := k.mcp.Toolset(s)
				if err != nil {
					mcpErrs = append(mcpErrs, fmt.Sprintf("MCP「%s」不可用: %v", s.Name, err))
				} else {
					toolsets = append(toolsets, ts)
				}
				break
			}
		}
		if !found {
			mcpErrs = append(mcpErrs, fmt.Sprintf("MCP 服务器 %q 不存在", id))
		}
	}
	if len(mcpErrs) > 0 {
		return nil, fmt.Errorf("agent %q: %s", ac.Name, strings.Join(mcpErrs, "；"))
	}

	lc := k.llmConfig(ac, m, tools, toolsets)
	lc.SubAgents = subAgents
	return llmagent.New(lc)
}

// llmConfig assembles the llmagent.Config for an agent config, using an
// InstructionProvider so braces in user prompts are never treated as state
// placeholders.
func (k *Kit) llmConfig(ac config.AgentConfig, m model.LLM, tools []tool.Tool, toolsets []tool.Toolset) llmagent.Config {
	instruction := applyPromptVars(ac.SystemPrompt, k.workspace())
	if strings.TrimSpace(instruction) == "" {
		instruction = "You are a helpful assistant."
	}
	gcc := &genai.GenerateContentConfig{}
	if ac.Temperature > 0 {
		t := float32(ac.Temperature)
		gcc.Temperature = &t
	}
	if ac.MaxTokens > 0 {
		gcc.MaxOutputTokens = int32(ac.MaxTokens)
	}
	return llmagent.Config{
		Name:                     sanitizeAgentName(ac.Name),
		Model:                    m,
		Description:              firstNonEmpty(ac.Description, ac.Name),
		InstructionProvider:      func(agent.ReadonlyContext) (string, error) { return instruction, nil },
		GenerateContentConfig:    gcc,
		Tools:                    tools,
		Toolsets:                 toolsets,
		DisallowTransferToParent: false,
	}
}

// BuildTeam builds the root agent for a team: lead agent with members as
// sub-agents (ADK handles delegation/transfer between them), or an
// auto-generated coordinator when requested.
func (k *Kit) BuildTeam(tc config.TeamConfig) (agent.Agent, string, error) {
	cfg := k.store.Get()

	memberByID := map[string]config.AgentConfig{}
	for _, a := range cfg.Agents {
		memberByID[a.ID] = a
	}

	var members []agent.Agent
	var memberNames []string
	for _, id := range tc.MemberAgentIDs {
		ac, ok := memberByID[id]
		if !ok {
			continue
		}
		a, err := k.buildLLMAgent(ac, cfg)
		if err != nil {
			return nil, "", err
		}
		members = append(members, a)
		memberNames = append(memberNames, sanitizeAgentName(ac.Name))
	}

	// Remote A2A members join as sub-agents the coordinator can transfer to.
	for _, id := range tc.RemoteMemberIDs {
		for _, r := range cfg.RemoteAgents {
			if r.ID != id {
				continue
			}
			ra, err := BuildRemoteAgent(r)
			if err != nil {
				return nil, "", err
			}
			members = append(members, ra)
			memberNames = append(memberNames, sanitizeAgentName(r.Name))
		}
	}

	rootName := sanitizeAgentName(tc.Name)

	// Explicit lead: use that agent as root; the other members become peers.
	if !tc.AutoCoordinate {
		if _, ok := memberByID[tc.LeadAgentID]; ok {
			var peers []agent.Agent
			for i, id := range tc.MemberAgentIDs {
				if id == tc.LeadAgentID {
					continue
				}
				peers = append(peers, members[i])
			}
			leadAc := memberByID[tc.LeadAgentID]
			leadAgent, err := k.buildLLMAgentWithSubAgents(leadAc, cfg, peers)
			if err != nil {
				return nil, "", err
			}
			return leadAgent, sanitizeAgentName(leadAc.Name), nil
		}
	}

	// Auto coordinator: synthesized team-lead agent.
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == tc.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return nil, "", fmt.Errorf("team %q: model provider not found for coordinator", tc.Name)
	}
	m, err := NewModelFromProvider(*provider, tc.Model)
	if err != nil {
		return nil, "", err
	}
	coordInstruction := firstNonEmpty(tc.SystemPrompt,
		"You are the coordinator of an agent team. Analyze the user's request, "+
			"then transfer the task to the most suitable member agent and synthesize the final answer.")
	instruction := fmt.Sprintf("%s\n\nTeam members: %s. Transfer to a member to work on the task, then summarize the result for the user.",
		coordInstruction, strings.Join(memberNames, ", "))

	coord, err := llmagent.New(llmagent.Config{
		Name:                rootName,
		Model:               m,
		Description:         firstNonEmpty(tc.Description, tc.Name),
		InstructionProvider: func(agent.ReadonlyContext) (string, error) { return instruction, nil },
		SubAgents:           members,
	})
	if err != nil {
		return nil, "", err
	}
	return coord, rootName, nil
}

func (k *Kit) workspace() string { return k.store.Get().Settings.WorkspaceDir }

// searchOptions assembles the knowledge tool options from current settings:
// workspace plus the configured embedding backend, if any.
func (k *Kit) searchOptions(cfg config.Config) SearchOptions {
	opts := SearchOptions{Workspace: cfg.Settings.WorkspaceDir, Index: k.knowledge}
	if cfg.Settings.EmbeddingProviderID == "" || cfg.Settings.EmbeddingModel == "" {
		return opts // keyword-only
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == cfg.Settings.EmbeddingProviderID {
			if cfg.Providers[i].Protocol == config.ProtocolAnthropic || cfg.Providers[i].Protocol == config.ProtocolGemini {
				return opts // embeddings endpoint is OpenAI-compatible only
			}
			opts.Embedding = &EmbeddingConfig{
				BaseURL: cfg.Providers[i].BaseURL,
				APIKey:  cfg.Providers[i].APIKey,
				Model:   cfg.Settings.EmbeddingModel,
			}
			break
		}
	}
	return opts
}

// skillToolset builds an ADK skill toolset covering the selected skills by
// materializing them as SKILL.md under a per-agent staging directory. Failing
// to write a skill is reported instead of silently running an agent that is
// missing a capability the user attached.
//
// The staging directory is scoped per agent (skills/agents/<key>): the ADK
// filesystem source exposes every skill in the tree it is pointed at, so a
// shared directory would leak skills between agents and keep unmounted or
// deleted skills loadable. Directories not in the wanted set are pruned before
// loading, so detaching or deleting a skill takes effect on the next build.
func (k *Kit) skillToolset(ac config.AgentConfig, cfg config.Config) (tool.Toolset, error) {
	ids := ac.SkillIDs
	want := map[string]bool{}
	wantNames := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, s := range cfg.Skills {
		if want[s.ID] {
			wantNames[sanitizeSkillName(s.Name)] = true
		}
	}
	dir := filepath.Join(k.store.SkillsDir(), "agents", agentSkillKey(ac))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}
	for _, s := range cfg.Skills {
		if !want[s.ID] {
			continue
		}
		dirSkill := filepath.Join(dir, sanitizeSkillName(s.Name))
		if err := os.MkdirAll(dirSkill, 0o755); err != nil {
			return nil, fmt.Errorf("create skill dir %q: %w", s.Name, err)
		}
		body, err := skill.Build(&skill.Frontmatter{Name: sanitizeSkillName(s.Name), Description: s.Description}, s.Content)
		if err != nil {
			return nil, fmt.Errorf("build skill %q: %w", s.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dirSkill, "SKILL.md"), body, 0o644); err != nil {
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
	}
	// Prune skills that are no longer attached to this agent (dangling IDs
	// included: they materialize nothing, so any leftover dir is stale).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() && !wantNames[e.Name()] {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	src := skill.NewFileSystemSource(os.DirFS(dir))
	preloaded, cleanup, err := skill.WithCompletePreloadSource(context.Background(), src)
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}
	_ = cleanup // sources live for the process lifetime
	ts, err := skilltoolset.New(context.Background(), skilltoolset.Config{Source: preloaded})
	if err != nil {
		return nil, fmt.Errorf("create skill toolset: %w", err)
	}
	return ts, nil
}

// agentSkillKey is the per-agent skill staging directory name. The stable
// agent ID is preferred; the sanitized name covers configs without an ID
// (tests, ad-hoc builds).
func agentSkillKey(ac config.AgentConfig) string {
	if ac.ID != "" {
		return ac.ID
	}
	return sanitizeAgentName(ac.Name)
}

// RemoveMaterializedSkill deletes the materialized SKILL.md directories of a
// skill: the per-agent staging dirs and the legacy shared layout. Best effort
// — the next agent build prunes stale entries anyway.
func RemoveMaterializedSkill(skillsDir, skillName string) {
	name := sanitizeSkillName(skillName)
	if agents, err := os.ReadDir(filepath.Join(skillsDir, "agents")); err == nil {
		for _, a := range agents {
			if a.IsDir() {
				_ = os.RemoveAll(filepath.Join(skillsDir, "agents", a.Name(), name))
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(skillsDir, name))
}

// applyPromptVars resolves template variables in system prompts:
// {{date}}, {{time}}, {{datetime}} and {{workspace}} (resolved at each agent
// build, i.e. per turn, so long-running apps stay current).
func applyPromptVars(prompt, workspace string) string {
	now := time.Now()
	return strings.NewReplacer(
		"{{datetime}}", now.Format("2006-01-02 15:04"),
		"{{date}}", now.Format("2006-01-02"),
		"{{time}}", now.Format("15:04"),
		"{{workspace}}", workspace,
	).Replace(prompt)
}

// SanitizeAgentName makes a safe ADK agent name (identifier-like). Exported
// so callers (tests, tooling) can predict the sub-agent name a team's
// coordinator will transfer to.
func SanitizeAgentName(name string) string { return sanitizeAgentName(name) }

// sanitizeAgentName makes a safe ADK agent name (identifier-like).
func sanitizeAgentName(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			sb.WriteRune('_')
		}
	}
	out := strings.Trim(sb.String(), "_")
	if out == "" {
		// Deterministic fallback for non-ASCII names.
		h := fnv.New32a()
		_, _ = h.Write([]byte(name))
		out = fmt.Sprintf("agent_%x", h.Sum32())
	}
	if out == "user" {
		out = "user_agent"
	}
	return out
}

// sanitizeSkillName makes a valid agentskills.io skill name:
// lowercase alphanumerics and hyphens only, no leading/trailing hyphen.
func sanitizeSkillName(name string) string {
	var sb strings.Builder
	prevHyphen := true // suppress leading hyphens
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevHyphen = false
		case (r == '-' || r == '_' || r == ' ') && !prevHyphen:
			sb.WriteRune('-')
			prevHyphen = true
		}
	}
	out := strings.TrimSuffix(sb.String(), "-")
	if out == "" {
		out = "skill"
	}
	return out
}

// GuardrailsEnabled reports whether guardrails are on for an agent (default on).
func GuardrailsEnabled(ac config.AgentConfig) bool {
	return ac.EnableGuardrails == nil || *ac.EnableGuardrails
}

// confirmsTool reports whether a builtin tool category was marked as needing
// human-in-the-loop confirmation for this agent.
func confirmsTool(ac config.AgentConfig, category string) bool {
	for _, c := range ac.ConfirmTools {
		if c == category {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Built-in tools

type timeArgs struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Asia/Shanghai; empty for local time"`
}
type timeResult struct {
	Now      string `json:"now"`
	Timezone string `json:"timezone"`
}

// workspaceFileTools builds the list/read/write set for the "files" category.
func workspaceFileTools(workspace string, requireConfirmation bool) ([]tool.Tool, error) {
	list, err := ListFilesTool(workspace)
	if err != nil {
		return nil, err
	}
	read, err := ReadFileTool(workspace)
	if err != nil {
		return nil, err
	}
	write, err := WriteFileTool(workspace, requireConfirmation)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{list, read, write}, nil
}

// TimeTool reports the current time.
func TimeTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "get_current_time",
		Description: "Get the current date and time, optionally in a specific IANA timezone.",
	}, func(ctx agent.Context, args timeArgs) (timeResult, error) {
		tz := time.Local
		name := "local"
		if args.Timezone != "" {
			loc, err := time.LoadLocation(args.Timezone)
			if err != nil {
				return timeResult{}, fmt.Errorf("unknown timezone %q", args.Timezone)
			}
			tz = loc
			name = args.Timezone
		}
		return timeResult{Now: time.Now().In(tz).Format(time.RFC3339), Timezone: name}, nil
	})
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"relative path inside the workspace"`
}

// SafeWorkspacePath resolves rel inside workspace and blocks traversal.
func SafeWorkspacePath(workspace, rel string) (string, error) {
	return safeWorkspacePath(workspace, rel)
}

func safeWorkspacePath(workspace, rel string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace directory is not configured")
	}
	absRoot, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(absRoot, rel))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(full, absRoot+string(filepath.Separator)) && full != absRoot {
		return "", fmt.Errorf("path escapes the workspace")
	}
	// The lexical check above cannot see symlinks: a link inside the
	// workspace pointing at ~/.ssh passes it. Resolve both sides and compare
	// again. A path that does not exist yet (write target, possibly with new
	// parent directories) is validated through its deepest existing ancestor.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err == nil {
		if !isWithin(realRoot, realFull) {
			return "", fmt.Errorf("path escapes the workspace")
		}
		return realFull, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	ancestor := filepath.Dir(full)
	for {
		realAncestor, aerr := filepath.EvalSymlinks(ancestor)
		if aerr == nil {
			if !isWithin(realRoot, realAncestor) {
				return "", fmt.Errorf("path escapes the workspace")
			}
			return full, nil
		}
		if !os.IsNotExist(aerr) {
			return "", aerr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", aerr
		}
		ancestor = parent
	}
}

// isWithin reports whether target is root itself or lives under it.
func isWithin(root, target string) bool {
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}

// ListFilesTool lists workspace files.
func ListFilesTool(workspace string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "list_workspace_files",
		Description: "List files and folders under a path inside the user's workspace.",
	}, func(ctx agent.Context, args pathArgs) (filesResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return filesResult{}, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return filesResult{}, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name()+"/")
			} else {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return filesResult{Entries: strings.Join(names, "\n")}, nil
	})
}

type readFileResult struct {
	Content string `json:"content"`
}

// ReadFileTool reads a file within the workspace.
func ReadFileTool(workspace string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_workspace_file",
		Description: "Read a text file inside the user's workspace (max 64KB).",
	}, func(ctx agent.Context, args pathArgs) (readFileResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return readFileResult{}, err
		}
		data, err := os.ReadFile(root)
		if err != nil {
			return readFileResult{}, err
		}
		const limit = 64 * 1024
		content := string(data)
		if len(content) > limit {
			// Byte-exact truncation could split a UTF-8 sequence (invalid JSON
			// payload down the line); back up to the nearest rune boundary.
			cut := limit
			for cut > 0 && !utf8.RuneStart(content[cut]) {
				cut--
			}
			content = content[:cut] + "\n...[truncated]"
		}
		return readFileResult{Content: content}, nil
	})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"relative path inside the workspace"`
	Content string `json:"content"`
}

type writeResult struct {
	OK    bool `json:"ok"`
	Bytes int  `json:"bytes"`
}

// WriteFileTool writes a file within the workspace. When requireConfirmation
// is true the tool asks the human operator to approve each call first (ADK
// HITL): the run pauses on an adk_request_confirmation interrupt and only
// proceeds after a FunctionResponse with {"confirmed": true}.
func WriteFileTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:                "write_workspace_file",
		Description:         "Create or overwrite a text file inside the user's workspace.",
		RequireConfirmation: requireConfirmation,
	}, func(ctx agent.Context, args writeArgs) (writeResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return writeResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
			return writeResult{}, err
		}
		if err := os.WriteFile(root, []byte(args.Content), 0o644); err != nil {
			return writeResult{}, err
		}
		return writeResult{OK: true, Bytes: len(args.Content)}, nil
	})
}
