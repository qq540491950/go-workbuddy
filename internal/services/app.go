package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

// appName/userID mirror ChatService's fixed local identity (single-user app).
const (
	appName = "workbuddy"
	userID  = "local"
)

// AppService exposes generic app info.
type AppService struct {
	S           *Services
	logPath     string
	auditPath   string
	sessionsDB  string
	knowledgeDB string
	gormDB      *gorm.DB
}

// SetLogPath points the diagnostics log reader at the app log file.
func (a *AppService) SetLogPath(path string) { a.logPath = path }

// SetStoragePaths records the database file locations for storage stats.
func (a *AppService) SetStoragePaths(sessionsDB, knowledgeDB string, gormDB *gorm.DB) {
	a.sessionsDB, a.knowledgeDB, a.gormDB = sessionsDB, knowledgeDB, gormDB
}

// NewAppService creates the service.
func NewAppService(s *Services) *AppService { return &AppService{S: s} }

// AppVersion is injected by main (single source of truth).
var AppVersion = "dev"

// AppInfo returns static app metadata.
func (a *AppService) AppInfo() map[string]string {
	return map[string]string{
		"name":    "WorkBuddy Agent",
		"version": AppVersion,
		"adk":     "google adk-go v2",
	}
}

// backupSchemaVersion is the export format version. Bump on breaking changes;
// ImportConfig rejects files from newer app versions.
const backupSchemaVersion = 1

// maxBackupBytes caps the import payload (10MB, far above any real config).
const maxBackupBytes = 10 << 20

// maxLogTailLines caps RecentLogs output.
const maxLogTailLines = 300

// SetToolAuditPath points the diagnostics reader at the tool audit JSONL.
func (a *AppService) SetToolAuditPath(path string) { a.auditPath = path }

// RecentLogs returns the last lines of the application log for in-app
// diagnostics.
func (a *AppService) RecentLogs(tail int) ([]string, error) {
	return readTail(a.logPath, tail)
}

// RecentToolAudit returns the last lines of the tool audit JSONL.
func (a *AppService) RecentToolAudit(tail int) ([]string, error) {
	return readTail(a.auditPath, tail)
}

// readTail returns the last non-empty lines of a text file.
func readTail(path string, tail int) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("路径未配置")
	}
	if tail <= 0 || tail > maxLogTailLines {
		tail = 200
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"（文件为空或尚未创建）"}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, nil
}

// StorageInfo reports on-disk sizes of the app databases.
type StorageInfo struct {
	SessionsDBBytes  int64 `json:"sessionsDbBytes"`
	KnowledgeDBBytes int64 `json:"knowledgeDbBytes"`
}

// StorageStats returns the current database file sizes (-1 when absent).
func (a *AppService) StorageStats() StorageInfo {
	info := StorageInfo{SessionsDBBytes: -1, KnowledgeDBBytes: -1}
	if st, err := os.Stat(a.sessionsDB); err == nil {
		info.SessionsDBBytes = st.Size()
	}
	if st, err := os.Stat(a.knowledgeDB); err == nil {
		info.KnowledgeDBBytes = st.Size()
	}
	return info
}

// VacuumSessions compacts the sessions database (reclaims space from
// trimmed/regenerated history). Safe while no run is active; callers race
// only on transient "database is locked" style errors which we surface.
func (a *AppService) VacuumSessions() error {
	if a.gormDB == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	return a.gormDB.Exec("VACUUM").Error
}

// MemoryInfo reports the long-term memory store size.
type MemoryInfo struct {
	Count int64 `json:"count"`
}

// MemoryStats returns the number of long-term memory entries.
func (a *AppService) MemoryStats() (*MemoryInfo, error) {
	if a.S.Memory == nil {
		return nil, fmt.Errorf("记忆服务不可用")
	}
	n, err := a.S.Memory.Count(appName, userID)
	if err != nil {
		return nil, err
	}
	return &MemoryInfo{Count: n}, nil
}

// MemoryClear wipes all long-term memory entries for the local user.
func (a *AppService) MemoryClear() error {
	if a.S.Memory == nil {
		return fmt.Errorf("记忆服务不可用")
	}
	return a.S.Memory.Clear(appName, userID)
}

// agentShare is the portable per-agent share document. The referenced
// provider id travels with it so the importer can re-link or flag it.
type agentShare struct {
	Version int                `json:"version"`
	Agent   config.AgentConfig `json:"agent"`
}

// ImportAgentResult is the import outcome: the stored agent plus any
// machine-local fixes applied during import (stripped dangling refs), so the
// UI can surface what was adjusted.
type ImportAgentResult struct {
	Agent    config.AgentConfig `json:"agent"`
	Warnings []string           `json:"warnings,omitempty"`
}

// ExportAgent writes one agent (without secrets of its own — agents carry no
// keys) as a shareable JSON file into <workspace>/exports/agents and returns
// the file path.
func (a *AppService) ExportAgent(agentID string) (string, error) {
	cfg := a.S.Store.Get()
	var agent *config.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == agentID {
			agent = &cfg.Agents[i]
			break
		}
	}
	if agent == nil {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	data, err := json.MarshalIndent(agentShare{Version: backupSchemaVersion, Agent: *agent}, "", "  ")
	if err != nil {
		return "", err
	}
	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// The agent name comes from config (and from imported share files), so it
	// may contain path separators. Sanitize it like chat exports do.
	name := sanitizeExportName(agent.Name)
	if name == "" {
		name = "agent"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.agent.json", name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ImportAgent adds an agent from a share document with a fresh ID. When an
// agent with the same name exists, a (2)/(3)… suffix is appended.
func (a *AppService) ImportAgent(data string) (ImportAgentResult, error) {
	if len(data) > maxBackupBytes {
		return ImportAgentResult{}, fmt.Errorf("文件过大")
	}
	var share agentShare
	if err := json.Unmarshal([]byte(data), &share); err != nil {
		return ImportAgentResult{}, fmt.Errorf("分享文件解析失败: %w", err)
	}
	if share.Version > backupSchemaVersion {
		return ImportAgentResult{}, fmt.Errorf("分享版本 v%d 高于当前支持的 v%d，请先升级应用", share.Version, backupSchemaVersion)
	}
	imported := share.Agent
	imported.ID = ""
	imported.CreatedAt = time.Time{}
	if imported.Name == "" {
		return ImportAgentResult{}, fmt.Errorf("分享文件缺少 Agent 名称")
	}
	// Strip references that do not exist on this machine BEFORE inserting:
	// the strict build contract fails every turn of the imported agent
	// otherwise, and the user has no way to know which refs to detach.
	cfg := a.S.Store.Get()
	var warnings []string
	knownSkills := make(map[string]bool, len(cfg.Skills))
	for _, s := range cfg.Skills {
		knownSkills[s.ID] = true
	}
	knownMCP := make(map[string]bool, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		knownMCP[s.ID] = true
	}
	var skillWarnings, mcpWarnings []string
	imported.SkillIDs, skillWarnings = stripMissingRefs(imported.SkillIDs, knownSkills, "技能")
	imported.MCPServerIDs, mcpWarnings = stripMissingRefs(imported.MCPServerIDs, knownMCP, "MCP 服务器")
	warnings = append(append(warnings, skillWarnings...), mcpWarnings...)
	err := a.S.Store.Update(func(cfg *config.Config) {
		nameExists := func(name string) bool {
			for _, ag := range cfg.Agents {
				if ag.Name == name {
					return true
				}
			}
			return false
		}
		base := imported.Name
		for n := 2; nameExists(imported.Name); n++ {
			imported.Name = fmt.Sprintf("%s（%d）", base, n)
		}
		imported.ID = newID("agent_")
		imported.CreatedAt = nowUTC()
		cfg.Agents = append(cfg.Agents, imported)
	})
	if err != nil {
		return ImportAgentResult{}, err
	}
	return ImportAgentResult{Agent: imported, Warnings: warnings}, nil
}

// stripMissingRefs keeps only ids present in known and reports one warning
// per stripped id. A nil/empty input is returned untouched.
func stripMissingRefs(ids []string, known map[string]bool, label string) (kept, warnings []string) {
	if len(ids) == 0 {
		return ids, nil
	}
	kept = make([]string, 0, len(ids))
	for _, id := range ids {
		if known[id] {
			kept = append(kept, id)
			continue
		}
		warnings = append(warnings, "已移除不存在的"+label+"引用: "+id)
	}
	return kept, warnings
}

// remoteShare is the portable per-remote-agent share document.
type remoteShare struct {
	Version int                       `json:"version"`
	Remote  *config.RemoteAgentConfig `json:"remote"`
}

// ExportRemoteAgent writes one remote A2A agent as a shareable JSON file
// into <workspace>/exports/remotes and returns the file path.
func (a *AppService) ExportRemoteAgent(id string) (string, error) {
	cfg := a.S.Store.Get()
	var remote *config.RemoteAgentConfig
	for i := range cfg.RemoteAgents {
		if cfg.RemoteAgents[i].ID == id {
			remote = &cfg.RemoteAgents[i]
			break
		}
	}
	if remote == nil {
		return "", fmt.Errorf("remote agent %q not found", id)
	}
	data, err := json.MarshalIndent(remoteShare{Version: backupSchemaVersion, Remote: remote}, "", "  ")
	if err != nil {
		return "", err
	}
	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports", "remotes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := sanitizeExportName(remote.Name)
	if name == "" {
		name = "remote"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.remote.json", name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ImportRemoteAgent adds a remote agent from a share document with a fresh
// ID; name conflicts get a numeric suffix.
func (a *AppService) ImportRemoteAgent(data string) (config.RemoteAgentConfig, error) {
	if len(data) > maxBackupBytes {
		return config.RemoteAgentConfig{}, fmt.Errorf("文件过大")
	}
	var share remoteShare
	if err := json.Unmarshal([]byte(data), &share); err != nil {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件解析失败: %w", err)
	}
	if share.Version > backupSchemaVersion {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享版本 v%d 高于当前支持的 v%d，请先升级应用", share.Version, backupSchemaVersion)
	}
	if share.Remote == nil {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件缺少 remote 字段")
	}
	imported := *share.Remote
	imported.ID = ""
	imported.CreatedAt = time.Time{}
	if imported.Name == "" {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件缺少名称")
	}
	err := a.S.Store.Update(func(cfg *config.Config) {
		nameExists := func(name string) bool {
			for _, r := range cfg.RemoteAgents {
				if r.Name == name {
					return true
				}
			}
			return false
		}
		base := imported.Name
		for n := 2; nameExists(imported.Name); n++ {
			imported.Name = fmt.Sprintf("%s（%d）", base, n)
		}
		imported.ID = newID("ra_")
		imported.CreatedAt = nowUTC()
		cfg.RemoteAgents = append(cfg.RemoteAgents, imported)
	})
	if err != nil {
		return config.RemoteAgentConfig{}, err
	}
	return imported, nil
}

// configBackup is the portable backup document. Sessions are intentionally
// excluded: they live in sessions.db and are never overwritten by an import.
type configBackup struct {
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exportedAt"`
	App        string         `json:"app"`
	Config     *config.Config `json:"config"`
}

// ExportConfig writes the current configuration (without chat sessions) as a
// portable JSON backup into <workspace>/exports and returns the file path.
// With includeSecrets=false all provider API keys are stripped so the file is
// safe to share.
func (a *AppService) ExportConfig(includeSecrets bool) (string, error) {
	// Get returns an independent copy; the masking below must never reach the
	// live config (that would blank the user's API keys in memory).
	cfg := a.S.Store.Get()
	cfg.Sessions = nil
	// OAuth tokens are secrets AND machine-local: never exported.
	cfg.MCPTokens = nil
	if !includeSecrets {
		for i := range cfg.Providers {
			cfg.Providers[i].APIKey = ""
		}
		for i := range cfg.MCPServers {
			cfg.MCPServers[i].AuthToken = ""
			// stdio Env and streamable Headers routinely carry secrets
			// (OPENAI_API_KEY, Authorization: Bearer …) — a "safe to share"
			// backup must not carry them either.
			cfg.MCPServers[i].Env = nil
			cfg.MCPServers[i].Headers = nil
		}
	}
	data, err := json.MarshalIndent(configBackup{
		Version:    backupSchemaVersion,
		ExportedAt: nowUTC(),
		App:        "workbuddy-agent",
		Config:     &cfg,
	}, "", "  ")
	if err != nil {
		return "", err
	}

	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	perm := os.FileMode(0o644)
	if includeSecrets {
		perm = 0o600 // contains API keys
	}
	// Distinct names: a masked (shareable) backup must never be mistaken for
	// the secrets-bearing one, and same-second exports must not overwrite
	// each other.
	suffix := "redacted"
	if includeSecrets {
		suffix = "secrets"
	}
	path := filepath.Join(dir, fmt.Sprintf("workbuddy-config-%s-%s.json", nowUTC().Format("20060102-150405"), suffix))
	if err := os.WriteFile(path, data, perm); err != nil {
		return "", err
	}
	return path, nil
}

// ImportConfig replaces the current configuration (providers, agents, remote
// agents, teams, skills, MCP servers, settings) with the backup's contents.
// Chat sessions are preserved and API keys come from the file as-is, so an
// unmasked backup restores connectivity directly.
func (a *AppService) ImportConfig(data string) error {
	if len(data) > maxBackupBytes {
		return fmt.Errorf("备份文件过大（%d 字节，上限 10MB）", len(data))
	}
	var bak configBackup
	if err := json.Unmarshal([]byte(data), &bak); err != nil {
		return fmt.Errorf("备份文件解析失败: %w", err)
	}
	if bak.Version > backupSchemaVersion {
		return fmt.Errorf("备份版本 v%d 高于当前支持的 v%d，请先升级应用", bak.Version, backupSchemaVersion)
	}
	if bak.Config == nil {
		return fmt.Errorf("备份文件缺少 config 字段")
	}
	imported := *bak.Config
	imported.Sessions = nil
	imported.NormalizeImported()
	// OAuth tokens are machine-local state, like sessions: an import must not
	// wipe the local cache (it used to force re-consent on every OAuth server).
	imported.MCPTokens = a.S.Store.Get().MCPTokens
	// Track which MCP servers vanish or change so their cached toolsets and
	// connections are dropped — same contract as SaveMCPServer.
	oldServers := a.S.Store.Get().MCPServers
	// Never import stale IDs into the live config: keep everything the file
	// carries, sessions excluded.
	if err := a.S.Store.Update(func(cfg *config.Config) {
		sessions := cfg.Sessions
		*cfg = imported
		cfg.Sessions = sessions
	}); err != nil {
		return err
	}
	importedByID := map[string]string{}
	for _, s := range imported.MCPServers {
		importedByID[s.ID] = string(mustJSON(s))
	}
	for _, o := range oldServers {
		if cur, ok := importedByID[o.ID]; !ok || cur != string(mustJSON(o)) {
			a.S.MCP.Disconnect(o.ID)
		}
	}
	return nil
}

// MCPService exposes MCP server management to the frontend.
type MCPService struct{ S *Services }

// NewMCPService creates the service.
func NewMCPService(s *Services) *MCPService { return &MCPService{S: s} }

// Status returns the current status of all configured MCP servers.
func (m *MCPService) Status() []mcpmgr.ServerStatus {
	cfg := m.S.Store.Get()
	return m.S.MCP.AllStatus(cfg.MCPServers)
}

// Connect starts/verifies the connection for one MCP server and returns its
// status including the discovered tool list.
func (m *MCPService) Connect(id string) (mcpmgr.ServerStatus, error) {
	cfg := m.S.Store.Get()
	for _, s := range cfg.MCPServers {
		if s.ID == id {
			return m.S.MCP.Connect(s), nil
		}
	}
	return mcpmgr.ServerStatus{}, fmt.Errorf("MCP 服务器 %q 不存在", id)
}

// Disconnect drops the connection state of one MCP server.
func (m *MCPService) Disconnect(id string) {
	m.S.MCP.Disconnect(id)
}
