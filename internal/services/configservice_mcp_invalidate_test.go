package services

import (
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestSaveMCPServerInvalidatesStaleConnection: editing a server's settings
// must drop the manager's cached toolset (the next turn rebuilds with the new
// transport); saving an identical config must keep it.
func TestSaveMCPServerInvalidatesStaleConnection(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mcpmgr.New()
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mgr), MCP: mgr}
	cs := NewConfigService(svc)

	orig := config.MCPServerConfig{ID: "mcp_1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}
	if err := cs.SaveMCPServer(orig); err != nil {
		t.Fatal(err)
	}
	ts1, err := mgr.Toolset(orig)
	if err != nil {
		t.Fatal(err)
	}

	// Unchanged save: the cached toolset survives.
	if err := cs.SaveMCPServer(orig); err != nil {
		t.Fatal(err)
	}
	ts2, err := mgr.Toolset(orig)
	if err != nil {
		t.Fatal(err)
	}
	if ts1 != ts2 {
		t.Fatal("unchanged config should keep the cached toolset")
	}

	// Changed settings: the stale toolset must be dropped.
	changed := orig
	changed.Args = []string{"-u"}
	if err := cs.SaveMCPServer(changed); err != nil {
		t.Fatal(err)
	}
	ts3, err := mgr.Toolset(changed)
	if err != nil {
		t.Fatal(err)
	}
	if ts3 == ts1 {
		t.Fatal("changed config must rebuild the toolset")
	}
}
