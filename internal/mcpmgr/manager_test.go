package mcpmgr

import (
	"fmt"
	"net"
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

// TestCloseAllClearsStateAndIsIdempotent pins the shutdown contract: one call
// drops every tracked server (cache + status), repeated calls are safe. The
// app's shutdown hook relies on this so stdio child processes do not outlive
// the desktop app as orphans.
func TestCloseAllClearsStateAndIsIdempotent(t *testing.T) {
	m := New()
	stdio := config.MCPServerConfig{ID: "srv1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}
	httpSrv := config.MCPServerConfig{ID: "srv2", Name: "H", Transport: config.MCPStreamableHTTP, URL: "http://127.0.0.1:1/mcp"}
	if _, err := m.Toolset(stdio); err != nil {
		t.Fatalf("Toolset stdio: %v", err)
	}
	if _, err := m.Toolset(httpSrv); err != nil {
		t.Fatalf("Toolset http: %v", err)
	}

	m.CloseAll()
	m.CloseAll() // idempotent: no panic, no state resurrection

	for _, cfg := range []config.MCPServerConfig{stdio, httpSrv} {
		ts1, err := m.Toolset(cfg)
		if err != nil {
			t.Fatalf("Toolset after CloseAll: %v", err)
		}
		m.Disconnect(cfg.ID)
		ts2, err := m.Toolset(cfg)
		if err != nil {
			t.Fatalf("Toolset rebuild: %v", err)
		}
		if ts1 == ts2 {
			t.Fatalf("server %s: expected a fresh toolset after CloseAll + Disconnect cycle", cfg.ID)
		}
	}
}

// TestDisconnectClearsStaleError: a stopped server must not keep reporting
// the error that caused the last disconnect.
func TestDisconnectClearsStaleError(t *testing.T) {
	m := New()
	m.setState(&ServerStatus{ID: "srv1", Name: "S", State: "error", Error: "boom"})
	m.Disconnect("srv1")
	st := m.Status("srv1", "S")
	if st.State != "stopped" || st.Error != "" {
		t.Fatalf("disconnect must clear stale error, got %+v", st)
	}
}

// TestOAuthTransportPortNotHeld pins the lazy-listen contract: building an
// OAuth transport must not keep the redirect port bound (a cached token makes
// the consent flow unreachable, and holding the port broke reconnects on the
// same configured redirect port with EADDRINUSE).
func TestOAuthTransportPortNotHeld(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	cfg := config.MCPServerConfig{
		ID: "srv-oauth", Name: "O", Transport: config.MCPStreamableHTTP,
		URL: "http://127.0.0.1:1/mcp", AuthType: "oauth2",
		OAuthClientID: "client-id", OAuthRedirectPort: port,
	}
	for i := 1; i <= 2; i++ {
		if _, err := buildTransport(cfg, nil, nil); err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
		// The port must be free again immediately after the build.
		again, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("port still held after build %d: %v", i, err)
		}
		again.Close()
	}
}
