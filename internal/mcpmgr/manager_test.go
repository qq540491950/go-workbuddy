package mcpmgr

import (
	"testing"

	"changeme/internal/config"
)

// TestToolsetCachedUntilDisconnect pins the manager contract: toolsets are
// keyed by server ID and reused across calls; Disconnect drops the cache so
// the next build creates a fresh transport (used by SaveMCPServer to stop
// stale connections after a config edit).
func TestToolsetCachedUntilDisconnect(t *testing.T) {
	m := New()
	cfg := config.MCPServerConfig{ID: "srv1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}

	ts1, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset: %v", err)
	}
	ts2, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset (cached): %v", err)
	}
	if ts1 != ts2 {
		t.Fatal("expected the cached toolset instance to be reused")
	}

	m.Disconnect(cfg.ID)
	ts3, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset after disconnect: %v", err)
	}
	if ts3 == ts1 {
		t.Fatal("expected a fresh toolset after Disconnect")
	}
}
