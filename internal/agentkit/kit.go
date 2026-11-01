package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

// ModelFactory builds the model.LLM for a provider/model pair.
func NewModelFromProvider(p config.ModelProvider, modelID string) (model.LLM, error) {
	if modelID == "" {
		return nil, fmt.Errorf("缺少模型 ID")
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
	// skillMu serializes skill materialization: parallel builds of the same
	// agent (parallel sessions rebuild per turn) must not interleave the
	// write/prune/preload sequence over the shared staging directory.
	skillMu sync.Mutex
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
	return k.BuildAgentWithModelAndWorkspace(ac, "", "")
}

func (k *Kit) buildLLMAgent(ac config.AgentConfig, cfg config.Config, workspace string) (agent.Agent, error) {
	return k.buildLLMAgentWithSubAgents(ac, cfg, nil, workspace)
}

// BuildAgentWithModel builds an agent pinned to a specific model of its
// provider (per-session model override; empty modelID falls back to the
// agent's configured model).
func (k *Kit) BuildAgentWithModel(ac config.AgentConfig, modelID string) (agent.Agent, error) {
	return k.BuildAgentWithModelAndWorkspace(ac, modelID, "")
}

// BuildAgentWithModelAndWorkspace is the session-aware build entry: model
// override and effective task workspace are resolved together so file tools
// and prompt variables cannot disagree about the directory.
func (k *Kit) BuildAgentWithModelAndWorkspace(ac config.AgentConfig, modelID, workspace string) (agent.Agent, error) {
	cfg := k.store.Get()
	if strings.TrimSpace(modelID) != "" {
		ac.Model = EffectiveModel(cfg, ac.ProviderID, ac.Model, modelID)
	}
	if strings.TrimSpace(workspace) == "" {
		workspace = cfg.Settings.WorkspaceDir
	}
	return k.buildLLMAgent(ac, cfg, workspace)
}

// EffectiveModel resolves the model a session actually runs: the per-session
// override when the provider offers it, the agent's configured model
// otherwise. The single clamp shared by agent building, stats pricing and the
// auto-title side call.
func EffectiveModel(cfg config.Config, providerID, configuredModel, override string) string {
	if override != "" && modelSupported(cfg, providerID, override) {
		return override
	}
	return configuredModel
}

// ModelSupported reports whether the provider offers the given model id.
// Exported for callers that only need the boolean check.
func ModelSupported(cfg config.Config, providerID, modelID string) bool {
	return modelSupported(cfg, providerID, modelID)
}

func modelSupported(cfg config.Config, providerID, modelID string) bool {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID != providerID {
			continue
		}
		for _, m := range cfg.Providers[i].Models {
			if m.ID == modelID {
				return true
			}
		}
	}
	return false
}

func (k *Kit) buildLLMAgentWithSubAgents(ac config.AgentConfig, cfg config.Config, subAgents []agent.Agent, workspace string) (agent.Agent, error) {
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == ac.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return nil, fmt.Errorf("Agent「%s」找不到模型供应商，请重新选择", ac.Name)
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
			builtin, err := workspaceFileTools(workspace, confirmsTool(ac, "files"))
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
		case "todos":
			t, err := TodosTool()
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
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

	// MCP servers. The per-server 启用 switch is authoritative: enabled
	// servers are injected into every session (direct chat included) without
	// per-agent mounting; disabled ones stay off unless an agent explicitly
	// mounts them. A server whose transport cannot even be built (missing
	// command/URL, OAuth misconfiguration) is reported instead of silently
	// running an agent that is missing tools — the same contract as the
	// skill toolset above.
	explicit := make(map[string]bool, len(ac.MCPServerIDs))
	for _, id := range ac.MCPServerIDs {
		explicit[id] = true
	}
	var mcpErrs []string
	seen := make(map[string]bool, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		seen[s.ID] = true
		if s.Disabled && !explicit[s.ID] {
			continue
		}
		ts, err := k.mcp.Toolset(s)
		if err != nil {
			mcpErrs = append(mcpErrs, fmt.Sprintf("MCP「%s」不可用: %v", s.Name, err))
		} else {
			toolsets = append(toolsets, ts)
		}
	}
	for _, id := range ac.MCPServerIDs {
		if !seen[id] {
			mcpErrs = append(mcpErrs, fmt.Sprintf("MCP 服务器 %q 不存在", id))
		}
	}
	if len(mcpErrs) > 0 {
		return nil, fmt.Errorf("agent %q: %s", ac.Name, strings.Join(mcpErrs, "；"))
	}

	lc := k.llmConfig(ac, m, tools, toolsets, workspace)
	lc.SubAgents = subAgents
	return llmagent.New(lc)
}

// llmConfig assembles the llmagent.Config for an agent config, using an
// InstructionProvider so braces in user prompts are never treated as state
// placeholders.
func (k *Kit) llmConfig(ac config.AgentConfig, m model.LLM, tools []tool.Tool, toolsets []tool.Toolset, workspace string) llmagent.Config {
	instruction := applyPromptVars(ac.SystemPrompt, workspace)
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
	if len(ac.StopSequences) > 0 {
		gcc.StopSequences = ac.StopSequences
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
	return k.BuildTeamWithWorkspace(tc, "")
}

// BuildTeamWithWorkspace builds a team whose local members use the session's
// task workspace.
func (k *Kit) BuildTeamWithWorkspace(tc config.TeamConfig, workspace string) (agent.Agent, string, error) {
	cfg := k.store.Get()
	if strings.TrimSpace(workspace) == "" {
		workspace = cfg.Settings.WorkspaceDir
	}

	memberByID := map[string]config.AgentConfig{}
	for _, a := range cfg.Agents {
		memberByID[a.ID] = a
	}

	var members []agent.Agent
	var memberNames []string
	// memberByKey maps agent/remote IDs to their built agent, so the explicit
	// lead path can assemble its peers without positional indexing (a dangling
	// member ID used to shift the slice and mis-assign or panic).
	memberByKey := map[string]agent.Agent{}
	for _, id := range tc.MemberAgentIDs {
		ac, ok := memberByID[id]
		if !ok {
			continue
		}
		a, err := k.buildLLMAgent(ac, cfg, workspace)
		if err != nil {
			return nil, "", err
		}
		members = append(members, a)
		memberNames = append(memberNames, sanitizeAgentName(ac.Name))
		memberByKey[id] = a
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
			memberByKey[id] = ra
		}
	}

	rootName := sanitizeAgentName(tc.Name)

	// Explicit lead: use that agent as root; the other members — local AND
	// remote — become transferable peers.
	if !tc.AutoCoordinate {
		if _, ok := memberByID[tc.LeadAgentID]; ok {
			var peers []agent.Agent
			for _, id := range tc.MemberAgentIDs {
				if id != tc.LeadAgentID {
					if a, ok := memberByKey[id]; ok {
						peers = append(peers, a)
					}
				}
			}
			for _, id := range tc.RemoteMemberIDs {
				if a, ok := memberByKey[id]; ok {
					peers = append(peers, a)
				}
			}
			leadAc := memberByID[tc.LeadAgentID]
			leadAgent, err := k.buildLLMAgentWithSubAgents(leadAc, cfg, peers, workspace)
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
		return nil, "", fmt.Errorf("团队「%s」的协调员缺少模型供应商", tc.Name)
	}
	m, err := NewModelFromProvider(*provider, tc.Model)
	if err != nil {
		return nil, "", err
	}
	coordInstruction := firstNonEmpty(tc.SystemPrompt,
		"You are the coordinator of an agent team. Analyze the user's request, "+
			"then transfer the task to the most suitable member agent and synthesize the final answer.")
	coordInstruction = applyPromptVars(coordInstruction, workspace)
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
	k.skillMu.Lock()
	defer k.skillMu.Unlock()
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
	// Same no-silent-degradation contract as MCP: a mounted skill that no
	// longer exists (imported share files are the usual source) is reported,
	// not quietly dropped — the agent would otherwise claim capabilities it
	// never had.
	var missing []string
	for _, id := range ids {
		exists := false
		for _, s := range cfg.Skills {
			if s.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("agent %q: 挂载的技能不存在（请重新选择或导入）: %s", ac.Name, strings.Join(missing, ", "))
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
		// Atomic replace: a concurrent preload must never read a half-written
		// (O_TRUNC) SKILL.md as valid-but-empty instructions.
		tmp := filepath.Join(dirSkill, "SKILL.md.tmp")
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
		if err := os.Rename(tmp, filepath.Join(dirSkill, "SKILL.md")); err != nil {
			_ = os.Remove(tmp)
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

// MaterializedSkillName returns the on-disk directory name for a skill.
// Exported so config validation can reject names that would collide with an
// existing skill's materialized directory.
func MaterializedSkillName(name string) string { return sanitizeSkillName(name) }

// RemoveMaterializedSkill deletes the materialized SKILL.md directories of a
// skill: the per-agent staging dirs and the legacy shared layout. Best effort
// — the next agent build prunes stale entries anyway. Runs under skillMu so a
// concurrent build's write/prune/preload sequence is never torn apart.
func (k *Kit) RemoveMaterializedSkill(skillName string) {
	k.skillMu.Lock()
	defer k.skillMu.Unlock()
	skillsDir := k.store.SkillsDir()
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
				return timeResult{}, fmt.Errorf("未知时区 %q", args.Timezone)
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

const (
	maxReadBytes  = 64 * 1024
	maxWriteBytes = 6 << 20 // single write cap, same order as image attachments
)

// ReadBoundedFile reads at most limit bytes from path, stopping as soon as
// the cap is reached (a huge file must not be loaded into memory just to be
// truncated). The returned bool reports truncation; truncated content is cut
// back to the nearest UTF-8 rune boundary and carries a trailing marker.
func ReadBoundedFile(path string, limit int) (content string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf := make([]byte, limit+1)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", false, readErr
	}
	truncated = n > limit
	if truncated {
		buf = buf[:limit]
		// Trim back to the last COMPLETE rune: a cut may land inside a
		// multi-byte sequence (a bare lead byte is RuneStart too).
		for len(buf) > 0 {
			if r, size := utf8.DecodeLastRune(buf); r != utf8.RuneError || size > 1 {
				break
			}
			buf = buf[:len(buf)-1]
		}
		content = string(buf) + "\n...[truncated]"
		return content, true, nil
	}
	return string(buf[:n]), false, nil
}

// WriteBoundedFile refuses payloads above limit bytes up front so a runaway
// generation cannot fill the disk; the file is not created on rejection.
func WriteBoundedFile(path, content string, limit int) error {
	if len(content) > limit {
		return fmt.Errorf("写入内容超过 %d MB 上限（%d 字节）", limit>>20, len(content))
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func safeWorkspacePath(workspace, rel string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("未配置工作区目录")
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
		return "", fmt.Errorf("路径越出工作区范围")
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
			return "", fmt.Errorf("路径越出工作区范围")
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
				return "", fmt.Errorf("路径越出工作区范围")
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
		content, _, err := ReadBoundedFile(root, maxReadBytes)
		if err != nil {
			return readFileResult{}, err
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
		Description:         "Create or overwrite a text file inside the user's workspace (single write up to 6MB).",
		RequireConfirmation: requireConfirmation,
	}, func(ctx agent.Context, args writeArgs) (writeResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return writeResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
			return writeResult{}, err
		}
		if err := WriteBoundedFile(root, args.Content, maxWriteBytes); err != nil {
			return writeResult{}, err
		}
		return writeResult{OK: true, Bytes: len(args.Content)}, nil
	})
}

// ---------------------------------------------------------------------------
// Session plan (todo list) tool

// todosStateKey stores the plan in the ADK session state so it survives
// restarts and stays scoped to the conversation.
const todosStateKey = "workbuddy.todos"

type todoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

type todosArgs struct {
	// Todos is the FULL replacement list. Omitted => read the current plan
	// without writing; empty => clear it.
	Todos []todoItem `json:"todos,omitempty"`
}

type todosResult struct {
	OK    bool       `json:"ok"`
	Todos []todoItem `json:"todos"`
}

// TodosTool gives the model a session-scoped plan (todo list) for multi-step
// work: it rewrites the list as steps are created, started and completed, and
// the user follows along on the tool-result card. Mirrors the plan tools of
// the coding-agent desktops this app takes after.
func TodosTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "plan_todos",
		Description: "Maintain the session's task plan (todo list). Pass the FULL list of steps with status pending/in_progress/completed each time — mark a step in_progress before starting it and completed right after finishing it, creating new steps as work emerges. Omit 'todos' to read the current plan. Use it for any multi-step task so the user can follow progress.",
	}, func(ctx agent.Context, args todosArgs) (todosResult, error) {
		if ctx.State() == nil {
			return todosResult{}, fmt.Errorf("会话状态不可用")
		}
		if args.Todos == nil {
			// Read-only: return the stored plan.
			cur, err := ctx.State().Get(todosStateKey)
			if err != nil {
				return todosResult{OK: true}, nil // no plan yet
			}
			var todos []todoItem
			b, _ := json.Marshal(cur)
			if json.Unmarshal(b, &todos) != nil {
				return todosResult{OK: true}, nil
			}
			return todosResult{OK: true, Todos: todos}, nil
		}
		cleaned := make([]todoItem, 0, len(args.Todos))
		for _, td := range args.Todos {
			td.Content = strings.TrimSpace(td.Content)
			if td.Content == "" {
				continue
			}
			switch td.Status {
			case "pending", "in_progress", "completed":
			default:
				td.Status = "pending"
			}
			cleaned = append(cleaned, td)
		}
		if err := ctx.State().Set(todosStateKey, cleaned); err != nil {
			return todosResult{}, err
		}
		return todosResult{OK: true, Todos: cleaned}, nil
	})
}
