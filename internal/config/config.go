// Package config defines the persistable configuration of the application:
// model providers, agents, agent teams, skills, MCP servers and global settings.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Protocol enumerates the LLM API protocols supported by the app.
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"    // OpenAI Chat Completions compatible (OpenAI, DeepSeek, Kimi, Ollama, vLLM...)
	ProtocolAnthropic Protocol = "anthropic" // Anthropic Messages API (Claude)
	ProtocolGemini    Protocol = "gemini"    // Google Gemini API
)

// ModelInfo describes a model exposed by a provider.
type ModelInfo struct {
	ID string `json:"id"`
	// Multimodal marks models that accept image input; the chat UI uses it
	// to enable the image attachment button.
	Multimodal bool `json:"multimodal,omitempty"`
	// ContextWindow is the model's input context size in tokens (0 = unknown).
	ContextWindow int `json:"contextWindow,omitempty"`
	// PriceIn/PriceOut are optional prices per 1M tokens (input/output),
	// used for cost estimation in the chat UI.
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
}

// UnmarshalJSON accepts both the legacy entries ("model-id") and the current
// object form, so old config files load unchanged.
func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err == nil {
		*m = ModelInfo{ID: id}
		return nil
	}
	type plain ModelInfo
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*m = ModelInfo(p)
	return nil
}

// ModelProvider describes a configurable LLM provider endpoint.
type ModelProvider struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Protocol  Protocol    `json:"protocol"`
	BaseURL   string      `json:"baseUrl"`
	APIKey    string      `json:"apiKey"`
	Models    []ModelInfo `json:"models"` // model ids usable with this provider
	IsDefault bool        `json:"isDefault"`
	// PriceIn/PriceOut are the legacy provider-level prices. They are copied
	// down to each model and cleared on load (migrateLegacy); kept only so
	// old config files migrate instead of losing data.
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
}

// AgentConfig describes a user-defined agent.
type AgentConfig struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	ProviderID   string    `json:"providerId"`
	Model        string    `json:"model"`
	SystemPrompt string    `json:"systemPrompt"`
	Temperature  float64   `json:"temperature"`
	MaxTokens    int       `json:"maxTokens"`
	SkillIDs     []string  `json:"skillIds"`     // skills attached to this agent
	MCPServerIDs []string  `json:"mcpServerIds"` // MCP servers whose tools are available
	BuiltinTools []string  `json:"builtinTools"` // subset of builtin tool names: time, files, knowledge
	CreatedAt    time.Time `json:"createdAt"`
	// EnableGuardrails masks secrets in user input/model output (default on
	// when unset). Pointers distinguish "unset" from explicit false.
	EnableGuardrails *bool `json:"enableGuardrails,omitempty"`
	// ConfirmTools lists builtin tool categories whose side-effectful calls
	// need human approval before execution (ADK HITL). Only "files" is
	// meaningful today: write_workspace_file asks for confirmation while
	// list/read stay automatic. Empty = never ask.
	ConfirmTools []string `json:"confirmTools,omitempty"`
	// Run guardrails against runaway loops/cost. 0 = unlimited.
	MaxTurnSeconds int `json:"maxTurnSeconds,omitempty"`
	MaxToolCalls   int `json:"maxToolCalls,omitempty"`
	// MaxSessionTokens caps cumulative tokens across the whole session
	// (0 = unlimited). The turn that crosses the budget still completes,
	// the next one is refused.
	MaxSessionTokens int64 `json:"maxSessionTokens,omitempty"`
	// StopSequences halts generation when the model emits one of them
	// (OpenAI "stop" / Anthropic "stop_sequences"). Empty = provider default.
	StopSequences []string `json:"stopSequences,omitempty"`
}

// RemoteAgentConfig describes a remote agent reached over the A2A protocol
// (Agent-To-Agent, a2aproject/a2a-go). The agent card is discovered from
// CardURL's well-known location.
type RemoteAgentConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CardURL     string    `json:"cardUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

// TeamConfig describes an agent team: a lead agent coordinating member agents.
type TeamConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	LeadAgentID    string   `json:"leadAgentId"`    // empty => auto coordinator
	AutoCoordinate bool     `json:"autoCoordinate"` // synthesize a coordinator instead of using a member as lead
	MemberAgentIDs []string `json:"memberAgentIds"`
	// RemoteMemberIDs reference RemoteAgents reachable over A2A; they act as
	// sub-agents the coordinator can transfer to. The lead is always local.
	RemoteMemberIDs []string  `json:"remoteMemberIds,omitempty"`
	ProviderID      string    `json:"providerId"` // used by the auto coordinator
	Model           string    `json:"model"`
	SystemPrompt    string    `json:"systemPrompt"`
	CreatedAt       time.Time `json:"createdAt"`
}

// SkillConfig is a reusable instruction bundle (agentskills.io SKILL.md).
type SkillConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"` // markdown body of the skill
	CreatedAt   time.Time `json:"createdAt"`
}

// MCPTransport selects how the MCP server is reached.
type MCPTransport string

const (
	MCPStdio          MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

// MCPServerConfig describes an MCP server connection.
type MCPServerConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Transport MCPTransport      `json:"transport"`
	Command   string            `json:"command"`           // stdio
	Args      []string          `json:"args"`              // stdio
	Env       map[string]string `json:"env,omitempty"`     // stdio
	URL       string            `json:"url"`               // streamable-http
	Headers   map[string]string `json:"headers,omitempty"` // streamable-http
	AutoStart bool              `json:"autoStart"`
	// Streamable-HTTP server authentication:
	// AuthType "" | "bearer" | "api-key" | "oauth2" (authorization code + PKCE
	// per the MCP authorization spec, handled by the official go-sdk handler).
	AuthType          string `json:"authType,omitempty"`
	AuthToken         string `json:"authToken,omitempty"`  // bearer token / api-key value
	AuthHeader        string `json:"authHeader,omitempty"` // api-key header name (default X-API-Key)
	OAuthClientID     string `json:"oauthClientId,omitempty"`
	OAuthRedirectPort int    `json:"oauthRedirectPort,omitempty"` // 0 => ephemeral port
}

// MCPOAuthToken is a cached OAuth token for an MCP server. TokenURL enables
// silent refresh across app restarts (no re-consent while the refresh token
// stays valid).
type MCPOAuthToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	TokenURL     string    `json:"tokenUrl,omitempty"`
}

// Settings holds global application settings.
type Settings struct {
	WorkspaceDir string `json:"workspaceDir"`
	Language     string `json:"language"`
	// Appearance: theme mode "light"|"dark"|"system", accent key, font scale
	// "sm"|"md"|"lg", and whether to speak assistant replies automatically.
	ThemeMode string `json:"themeMode"`
	Accent    string `json:"accent"`
	FontScale string `json:"fontScale"`
	AutoSpeak bool   `json:"autoSpeak"`
	// Semantic knowledge search: an OpenAI-compatible provider id + embedding
	// model. Empty keeps keyword-only search.
	EmbeddingProviderID string `json:"embeddingProviderId,omitempty"`
	EmbeddingModel      string `json:"embeddingModel,omitempty"`
	// A2A server auto-start: publish this agent on launch (port 0 = ephemeral).
	A2AAutoStart bool   `json:"a2aAutoStart,omitempty"`
	A2AAgentID   string `json:"a2aAgentId,omitempty"`
	A2APort      int    `json:"a2aPort,omitempty"`
	// ToolsAudit writes a JSONL audit trail of every tool call/result to the
	// app logs directory.
	ToolsAudit bool `json:"toolsAudit,omitempty"`
	// AutoRemember ingests every completed turn into long-term memory
	// automatically (best-effort; failures are silent).
	AutoRemember bool `json:"autoRemember,omitempty"`
	// AutoTitle generates a short conversation title with the model after the
	// first exchange. Opt-in (nil/false = off): the side model call would
	// otherwise surprise every scripted model integration.
	AutoTitle *bool `json:"autoTitle,omitempty"`
	// HTTPProxy is a global outbound proxy URL (e.g. http://127.0.0.1:7890)
	// applied to model/embedding/MCP requests at startup. Empty = direct.
	HTTPProxy string `json:"httpProxy,omitempty"`
	// Context compaction (ADK session/compaction). Interval counts completed
	// user turns between sliding-window summarizations; TokenThreshold fires
	// intra-turn tail retention at the given prompt token count. 0 = off.
	CompactionInterval       int `json:"compactionInterval,omitempty"`
	CompactionTokenThreshold int `json:"compactionTokenThreshold,omitempty"`
}

// AutoTitleEnabled reports whether model-generated titles are wanted.
func (s *Settings) AutoTitleEnabled() bool {
	return s.AutoTitle != nil && *s.AutoTitle
}

// normalize fills zero values (older config files) with defaults.
func (s *Settings) normalize() {
	if s.ThemeMode == "" {
		s.ThemeMode = "dark"
	}
	if s.Accent == "" {
		s.Accent = "blue"
	}
	if s.FontScale == "" {
		s.FontScale = "md"
	}
}

// Config is the root configuration document persisted to disk.
type Config struct {
	Settings     Settings            `json:"settings"`
	Providers    []ModelProvider     `json:"providers"`
	Agents       []AgentConfig       `json:"agents"`
	RemoteAgents []RemoteAgentConfig `json:"remoteAgents,omitempty"`
	Teams        []TeamConfig        `json:"teams"`
	// MCPTokens caches OAuth tokens for streamable-http MCP servers
	// (server ID -> token). Secrets live in this 0600 file only.
	MCPTokens  map[string]MCPOAuthToken `json:"mcpTokens,omitempty"`
	Skills     []SkillConfig            `json:"skills"`
	MCPServers []MCPServerConfig        `json:"mcpServers"`
	Sessions   []ChatSession            `json:"sessions"`
}

// ChatSession is a chat conversation bound to an agent, remote agent or team.
type ChatSession struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	TargetType string    `json:"targetType"` // "agent" | "team" | "remote"
	TargetID   string    `json:"targetId"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// ForkedFrom is the original session ID when this chat was created via
	// ForkSession (branching). Empty for organic sessions.
	ForkedFrom string `json:"forkedFrom,omitempty"`
	// Pinned sessions sort first in the sidebar.
	Pinned bool `json:"pinned,omitempty"`
	// Preview is the tail of the latest assistant reply, for the sidebar.
	Preview string `json:"preview,omitempty"`
	// Archived sessions are hidden from the main sidebar list.
	Archived bool `json:"archived,omitempty"`
	// ModelOverride pins this chat to a specific model of the target agent's
	// provider (empty = the agent's configured model). Mirrors the per-chat
	// model picker of the desktop AI clients.
	ModelOverride string `json:"modelOverride,omitempty"`
}

// Clone returns an independent copy of the config. Slices, nested slices and
// maps are duplicated, so mutating the clone (e.g. blanking API keys for a
// secret-free export) never touches the live config or its backing arrays.
func (c Config) Clone() Config {
	out := c
	out.Providers = make([]ModelProvider, len(c.Providers))
	for i, p := range c.Providers {
		p.Models = append([]ModelInfo(nil), p.Models...)
		out.Providers[i] = p
	}
	if c.Agents != nil {
		out.Agents = make([]AgentConfig, len(c.Agents))
		for i, a := range c.Agents {
			a.SkillIDs = append([]string(nil), a.SkillIDs...)
			a.MCPServerIDs = append([]string(nil), a.MCPServerIDs...)
			a.BuiltinTools = append([]string(nil), a.BuiltinTools...)
			a.ConfirmTools = append([]string(nil), a.ConfirmTools...)
			a.StopSequences = append([]string(nil), a.StopSequences...)
			if a.EnableGuardrails != nil {
				v := *a.EnableGuardrails
				a.EnableGuardrails = &v
			}
			out.Agents[i] = a
		}
	}
	if c.Teams != nil {
		out.Teams = make([]TeamConfig, len(c.Teams))
		for i, t := range c.Teams {
			t.MemberAgentIDs = append([]string(nil), t.MemberAgentIDs...)
			t.RemoteMemberIDs = append([]string(nil), t.RemoteMemberIDs...)
			out.Teams[i] = t
		}
	}
	if c.MCPServers != nil {
		out.MCPServers = make([]MCPServerConfig, len(c.MCPServers))
		for i, m := range c.MCPServers {
			m.Args = append([]string(nil), m.Args...)
			m.Env = cloneStringMap(m.Env)
			m.Headers = cloneStringMap(m.Headers)
			out.MCPServers[i] = m
		}
	}
	out.RemoteAgents = append([]RemoteAgentConfig(nil), c.RemoteAgents...)
	out.Skills = append([]SkillConfig(nil), c.Skills...)
	out.Sessions = append([]ChatSession(nil), c.Sessions...)
	out.MCPTokens = make(map[string]MCPOAuthToken, len(c.MCPTokens))
	for k, v := range c.MCPTokens {
		out.MCPTokens[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Store persists Config as JSON and guards concurrent access.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func defaultWorkspace() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "WorkBuddyWorkspace")
}

// Default returns a config seeded with useful defaults.
func Default() Config {
	return Config{
		Settings: Settings{
			WorkspaceDir: defaultWorkspace(),
			Language:     "zh-CN",
			ThemeMode:    "dark",
			Accent:       "blue",
			FontScale:    "md",
		},
		Skills: []SkillConfig{
			{
				ID:          "skill-weekly-report",
				Name:        "weekly-report",
				Description: "根据本周工作记录撰写结构化周报",
				Content: `# 周报撰写技能

按照以下结构撰写周报：
1. 本周完成事项（按优先级排列，量化成果）
2. 进行中事项（注明进度百分比与风险）
3. 下周计划（明确目标与衡量标准）
4. 需要协调的问题

语气专业简洁，使用中文，避免空话。`,
			},
			{
				ID:          "skill-code-review",
				Name:        "code-review",
				Description: "系统化代码审查清单与输出格式",
				Content: `# 代码审查技能

审查代码时依次检查：
- 正确性：边界条件、错误处理、并发安全
- 可读性：命名、函数长度、注释质量
- 设计：职责划分、重复代码、可测试性
- 安全：输入校验、敏感信息、权限

输出格式：按严重程度（P0 阻断 / P1 应修 / P2 建议）分组列出问题，每条给出文件、位置与修复建议。`,
			},
		},
	}
}

// migrateLegacy converges older config schemas onto the current one: legacy
// provider-level prices move down to each model, and nameless model entries
// are dropped. Runs once per load; the result is persisted by Open.
func (c *Config) migrateLegacy() {
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.PriceIn > 0 || p.PriceOut > 0 {
			for j := range p.Models {
				if p.Models[j].PriceIn == 0 {
					p.Models[j].PriceIn = p.PriceIn
				}
				if p.Models[j].PriceOut == 0 {
					p.Models[j].PriceOut = p.PriceOut
				}
			}
			p.PriceIn, p.PriceOut = 0, 0
		}
		models := p.Models[:0]
		for _, m := range p.Models {
			if m.ID != "" {
				models = append(models, m)
			}
		}
		p.Models = models
	}
}

// Open loads (or creates) the config store under dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "config.json"), cfg: Default()}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.cfg); err != nil {
			// Corrupt config: keep the file as backup and start fresh. The
			// backup may contain API keys, so it gets the same 0600 as the
			// live config (WriteFile does not tighten an existing file's
			// mode, hence the explicit Chmod).
			if werr := os.WriteFile(s.path+".bak", data, 0o600); werr == nil {
				_ = os.Chmod(s.path+".bak", 0o600)
			}
			s.cfg = Default()
		}
	}
	s.cfg.Settings.normalize()
	s.cfg.migrateLegacy()
	// Persist normalized defaults so old config files converge on the new schema.
	if _, err := os.Stat(s.path); err == nil {
		_ = s.saveLocked()
	}
	return s, nil
}

// NormalizeImported applies the same load-time normalization (settings
// defaults + legacy price migration) that Open gives every persisted config,
// so a backup produced by an older schema imports cleanly.
func (c *Config) NormalizeImported() {
	c.Settings.normalize()
	c.migrateLegacy()
}

// Dir returns the directory holding the config file.
func (s *Store) Dir() string { return filepath.Dir(s.path) }

// SkillsDir returns the directory where skills are materialized as SKILL.md.
func (s *Store) SkillsDir() string { return filepath.Join(s.Dir(), "skills") }

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return s.writeLocked(data)
}

// writeLocked atomically replaces the config file with the given bytes.
func (s *Store) writeLocked(data []byte) error {
	tmp := s.path + ".tmp"
	// 0600: the file contains API keys.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// Get returns a deep copy of the current config. Callers may mutate the
// result freely — slices and maps do not share storage with the live config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// Settings returns a copy of the global settings. Settings contains only value
// fields, so this is cheaper than Get for hot read paths.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Settings
}

// Update applies fn to the config under lock and persists it.
// Update applies fn and persists the result. The mutation is applied to a
// deep copy and only swapped into the live config after the disk write
// succeeds: a failed save (disk full, permissions) used to leave memory
// diverged from disk until the next restart silently reverted it.
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg.Clone()
	fn(&next)
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := s.writeLocked(data); err != nil {
		return err
	}
	s.cfg = next
	return nil
}
