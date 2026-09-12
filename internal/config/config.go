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
	ProtocolOpenAI     Protocol = "openai"     // OpenAI Chat Completions compatible (OpenAI, DeepSeek, Kimi, Ollama, vLLM...)
	ProtocolAnthropic  Protocol = "anthropic"  // Anthropic Messages API (Claude)
	ProtocolGemini     Protocol = "gemini"     // Google Gemini API
)

// ModelProvider describes a configurable LLM provider endpoint.
type ModelProvider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Protocol  Protocol `json:"protocol"`
	BaseURL   string   `json:"baseUrl"`
	APIKey    string   `json:"apiKey"`
	Models    []string `json:"models"` // model ids usable with this provider
	IsDefault bool     `json:"isDefault"`
}

// AgentConfig describes a user-defined agent.
type AgentConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ProviderID   string   `json:"providerId"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"systemPrompt"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"maxTokens"`
	SkillIDs     []string `json:"skillIds"`     // skills attached to this agent
	MCPServerIDs []string `json:"mcpServerIds"` // MCP servers whose tools are available
	BuiltinTools []string `json:"builtinTools"` // subset of builtin tool names: time, files
	CreatedAt    time.Time `json:"createdAt"`
}

// TeamConfig describes an agent team: a lead agent coordinating member agents.
type TeamConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	LeadAgentID    string   `json:"leadAgentId"`    // empty => auto coordinator
	AutoCoordinate bool     `json:"autoCoordinate"` // synthesize a coordinator instead of using a member as lead
	MemberAgentIDs []string `json:"memberAgentIds"`
	ProviderID     string   `json:"providerId"` // used by the auto coordinator
	Model          string   `json:"model"`
	SystemPrompt   string   `json:"systemPrompt"`
	CreatedAt      time.Time `json:"createdAt"`
}

// SkillConfig is a reusable instruction bundle (agentskills.io SKILL.md).
type SkillConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"` // markdown body of the skill
	CreatedAt   time.Time `json:"createdAt"`
}

// MCPTransport selects how the MCP server is reached.
type MCPTransport string

const (
	MCPStdio   MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

// MCPServerConfig describes an MCP server connection.
type MCPServerConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Transport MCPTransport      `json:"transport"`
	Command   string            `json:"command"`          // stdio
	Args      []string          `json:"args"`             // stdio
	Env       map[string]string `json:"env,omitempty"`    // stdio
	URL       string            `json:"url"`              // streamable-http
	Headers   map[string]string `json:"headers,omitempty"` // streamable-http
	AutoStart bool              `json:"autoStart"`
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
	Settings   Settings         `json:"settings"`
	Providers  []ModelProvider  `json:"providers"`
	Agents     []AgentConfig    `json:"agents"`
	Teams      []TeamConfig     `json:"teams"`
	Skills     []SkillConfig    `json:"skills"`
	MCPServers []MCPServerConfig `json:"mcpServers"`
	Sessions   []ChatSession    `json:"sessions"`
}

// ChatSession is a chat conversation bound to an agent or a team.
type ChatSession struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	TargetType string    `json:"targetType"` // "agent" | "team"
	TargetID   string    `json:"targetId"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
			// Corrupt config: keep the file as backup and start fresh.
			_ = os.WriteFile(s.path+".bak", data, 0o644)
			s.cfg = Default()
		}
	}
	s.cfg.Settings.normalize()
	// Persist normalized defaults so old config files converge on the new schema.
	if _, err := os.Stat(s.path); err == nil {
		_ = s.saveLocked()
	}
	return s, nil
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
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Get returns a deep copy of the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update applies fn to the config under lock and persists it.
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	return s.saveLocked()
}
