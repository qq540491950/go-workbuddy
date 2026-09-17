package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

// AppService exposes generic app info.
type AppService struct{ S *Services }

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

// configBackup is the portable backup document. Sessions are intentionally
// excluded: they live in sessions.db and are never overwritten by an import.
type configBackup struct {
	Version    int           `json:"version"`
	ExportedAt time.Time     `json:"exportedAt"`
	App        string        `json:"app"`
	Config     *config.Config `json:"config"`
}

// ExportConfig writes the current configuration (without chat sessions) as a
// portable JSON backup into <workspace>/exports and returns the file path.
// With includeSecrets=false all provider API keys are stripped so the file is
// safe to share.
func (a *AppService) ExportConfig(includeSecrets bool) (string, error) {
	cfg := a.S.Store.Get()
	cfg.Sessions = nil
	if !includeSecrets {
		for i := range cfg.Providers {
			cfg.Providers[i].APIKey = ""
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
	path := filepath.Join(dir, fmt.Sprintf("workbuddy-config-%s.json", nowUTC().Format("20060102-150405")))
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
	// Never import stale IDs into the live config: keep everything the file
	// carries, sessions excluded.
	return a.S.Store.Update(func(cfg *config.Config) {
		sessions := cfg.Sessions
		*cfg = imported
		cfg.Sessions = sessions
	})
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
	return mcpmgr.ServerStatus{}, nil
}

// Disconnect drops the connection state of one MCP server.
func (m *MCPService) Disconnect(id string) {
	m.S.MCP.Disconnect(id)
}
