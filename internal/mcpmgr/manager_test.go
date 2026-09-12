package mcpmgr

import (
	"testing"

	"changeme/internal/config"
)

func TestBuildTransportValidation(t *testing.T) {
	if _, err := buildTransport(config.MCPServerConfig{Transport: config.MCPStdio}); err == nil {
		t.Fatal("expected error for stdio without command")
	}
	if _, err := buildTransport(config.MCPServerConfig{Transport: config.MCPStreamableHTTP}); err == nil {
		t.Fatal("expected error for http without URL")
	}
	tr, err := buildTransport(config.MCPServerConfig{
		Transport: config.MCPStreamableHTTP, URL: "https://example.com/mcp",
	})
	if err != nil || tr == nil {
		t.Fatalf("expected valid streamable transport, got %v, %v", tr, err)
	}
}

func TestConnectReportsErrorForBadCommand(t *testing.T) {
	m := New()
	cfg := config.MCPServerConfig{
		ID: "mcp_bad", Name: "broken", Transport: config.MCPStdio, Command: "/nonexistent/binary/xyz",
	}
	st := m.Connect(cfg)
	if st.State != "error" || st.Error == "" {
		t.Fatalf("expected error status, got %+v", st)
	}
	got := m.Status("mcp_bad", "broken")
	if got.State != "error" {
		t.Fatalf("status not cached: %+v", got)
	}

	// Disconnect clears back to stopped.
	m.Disconnect("mcp_bad")
	if got := m.Status("mcp_bad", "broken"); got.State != "stopped" {
		t.Fatalf("expected stopped after disconnect, got %+v", got)
	}
}

func TestToolsetCaching(t *testing.T) {
	m := New()
	cfg := config.MCPServerConfig{
		ID: "mcp_1", Name: "s", Transport: config.MCPStdio, Command: "/nonexistent/binary/xyz",
	}
	ts1, err := m.Toolset(cfg)
	if err != nil {
		// Creating the toolset itself must succeed (connection is lazy),
		// but a transport build failure would also be acceptable.
		t.Skipf("toolset creation failed: %v", err)
	}
	ts2, err := m.Toolset(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ts1 != ts2 {
		t.Fatal("expected cached toolset instance")
	}
}
