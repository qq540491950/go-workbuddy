package agentkit

import (
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

// TestBuildAgentInjectsEnabledMCPServers: the per-server 启用 switch is the
// authoritative injection control — enabled servers reach every session
// (direct chat included) without per-agent mounting, disabled ones stay off
// unless an agent explicitly mounts them. A broken transport on an enabled
// server must surface as a build error (no silent tool loss); the same broken
// transport disabled must build cleanly.
func TestBuildAgentInjectsEnabledMCPServers(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.MCPServers = append(cfg.MCPServers,
			config.MCPServerConfig{ID: "mcp_on", Name: "启用服务", Transport: config.MCPStdio, Command: ""},
			config.MCPServerConfig{ID: "mcp_off", Name: "停用服务", Transport: config.MCPStdio, Disabled: true, Command: ""},
		)
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "a1", Name: "工具人", ProviderID: "p1", Model: "m1"}

	// Enabled (broken) server: must be mounted and reported.
	if _, err := kit.BuildAgent(ac); err == nil {
		t.Fatal("enabled MCP server must be injected (broken transport must surface)")
	} else if !strings.Contains(err.Error(), "启用服务") {
		t.Fatalf("error missing enabled server: %v", err)
	}

	// Disabled (broken) server alone: must stay off and build cleanly.
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.MCPServers {
			if cfg.MCPServers[i].ID == "mcp_on" {
				cfg.MCPServers[i].Disabled = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.BuildAgent(ac); err != nil {
		t.Fatalf("disabled MCP server must not be injected: %v", err)
	}

	// Escape hatch: an agent explicitly mounting a disabled server opts back in.
	ac.MCPServerIDs = []string{"mcp_off"}
	if _, err := kit.BuildAgent(ac); err == nil || !strings.Contains(err.Error(), "停用服务") {
		t.Fatalf("explicit mount of a disabled server must be honored, got: %v", err)
	}
}
