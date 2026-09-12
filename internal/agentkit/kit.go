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
	store  *config.Store
	mcp    *mcpmgr.Manager
	memory tool.Tool // long-term memory search tool (nil if memory unavailable)
}

// NewKit creates a Kit.
func NewKit(store *config.Store, mcp *mcpmgr.Manager) *Kit {
	return &Kit{store: store, mcp: mcp}
}

// SetMemoryTool attaches the long-term memory search tool.
func (k *Kit) SetMemoryTool(t tool.Tool) { k.memory = t }

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
			tools = append(tools, TimeTool())
		case "files":
			tools = append(tools, ListFilesTool(k.workspace()), ReadFileTool(k.workspace()), WriteFileTool(k.workspace()))
		case "knowledge":
			tools = append(tools, SearchKnowledgeTool(k.workspace()))
		case "memory":
			if k.memory != nil {
				tools = append(tools, k.memory)
			}
		}
	}

	// Skills (progressive disclosure via ADK skill toolset).
	if len(ac.SkillIDs) > 0 {
		if ts := k.skillToolset(ac.SkillIDs, cfg); ts != nil {
			toolsets = append(toolsets, ts)
		}
	}

	// MCP servers.
	for _, id := range ac.MCPServerIDs {
		for _, s := range cfg.MCPServers {
			if s.ID == id {
				ts, err := k.mcp.Toolset(s)
				if err == nil {
					toolsets = append(toolsets, ts)
				}
				break
			}
		}
	}

	lc := k.llmConfig(ac, m, tools, toolsets)
	lc.SubAgents = subAgents
	return llmagent.New(lc)
}

// llmConfig assembles the llmagent.Config for an agent config, using an
// InstructionProvider so braces in user prompts are never treated as state
// placeholders.
func (k *Kit) llmConfig(ac config.AgentConfig, m model.LLM, tools []tool.Tool, toolsets []tool.Toolset) llmagent.Config {
	instruction := ac.SystemPrompt
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
		Name:                  sanitizeAgentName(ac.Name),
		Model:                 m,
		Description:           firstNonEmpty(ac.Description, ac.Name),
		InstructionProvider:   func(agent.ReadonlyContext) (string, error) { return instruction, nil },
		GenerateContentConfig: gcc,
		Tools:                 tools,
		Toolsets:              toolsets,
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

// skillToolset builds an ADK skill toolset covering the selected skills by
// materializing them as SKILL.md under the app skills directory.
func (k *Kit) skillToolset(ids []string, cfg config.Config) tool.Toolset {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	dir := k.store.SkillsDir()
	_ = os.MkdirAll(dir, 0o755)
	for _, s := range cfg.Skills {
		if !want[s.ID] {
			continue
		}
		dirSkill := filepath.Join(dir, sanitizeSkillName(s.Name))
		_ = os.MkdirAll(dirSkill, 0o755)
		body, err := skill.Build(&skill.Frontmatter{Name: sanitizeSkillName(s.Name), Description: s.Description}, s.Content)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(dirSkill, "SKILL.md"), body, 0o644)
	}
	src := skill.NewFileSystemSource(os.DirFS(dir))
	preloaded, cleanup, err := skill.WithCompletePreloadSource(context.Background(), src)
	if err != nil {
		return nil
	}
	_ = cleanup // sources live for the process lifetime
	ts, err := skilltoolset.New(context.Background(), skilltoolset.Config{Source: preloaded})
	if err != nil {
		return nil
	}
	return ts
}

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

// TimeTool reports the current time.
func TimeTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
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
	return t
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"relative path inside the workspace"`
}

type filesResult struct {
	Entries string `json:"entries"`
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
	return full, nil
}

// ListFilesTool lists workspace files.
func ListFilesTool(workspace string) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
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
	return t
}

type readFileResult struct {
	Content string `json:"content"`
}

// ReadFileTool reads a file within the workspace.
func ReadFileTool(workspace string) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
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
			content = content[:limit] + "\n...[truncated]"
		}
		return readFileResult{Content: content}, nil
	})
	return t
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"relative path inside the workspace"`
	Content string `json:"content"`
}

type writeResult struct {
	OK     bool `json:"ok"`
	Bytes  int  `json:"bytes"`
}

// WriteFileTool writes a file within the workspace.
func WriteFileTool(workspace string) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "write_workspace_file",
		Description: "Create or overwrite a text file inside the user's workspace.",
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
	return t
}
