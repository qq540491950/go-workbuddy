package services

import (
	"time"

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
