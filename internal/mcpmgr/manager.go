// Package mcpmgr manages MCP server connections for the app:
// it owns one adk mcptoolset per configured server and tracks status.
package mcpmgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
	"google.golang.org/genai"

	"changeme/internal/config"
)

// ToolInfo describes a tool exposed by a connected MCP server.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ServerStatus is the runtime status of one configured MCP server.
type ServerStatus struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	State     string     `json:"state"` // stopped | connected | error
	Tools     []ToolInfo `json:"tools"`
	Error     string     `json:"error,omitempty"`
	CheckedAt time.Time  `json:"checkedAt"`
}

// Manager owns MCP toolsets keyed by server config ID.
type Manager struct {
	mu         sync.Mutex
	toolsets   map[string]tool.Toolset
	conns      map[string]*trackedTransport // closeable connections per server
	states     map[string]*ServerStatus
	tokenStore *config.Store // optional: persists OAuth tokens across restarts
	// onAuthPending, when set, is notified while an OAuth consent flow waits
	// for the user in the browser (server display name + authorize URL).
	onAuthPending func(server, authURL string)
}

// trackedTransport wraps an MCP transport and remembers the connection it
// opens, so Disconnect can actually close the session (and with it the stdio
// child process). The ADK toolset keeps its connection private, so this
// wrapper is the only closeable handle we own.
type trackedTransport struct {
	inner mcp.Transport
	mu    sync.Mutex
	conn  mcp.Connection
}

func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	prev := t.conn
	t.conn = conn
	t.mu.Unlock()
	// The SDK reconnects by calling Connect again after a broken session;
	// drop the stale one so repeated reconnects do not accumulate sessions.
	if prev != nil {
		_ = prev.Close()
	}
	return conn, nil
}

func (t *trackedTransport) Close() error {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// SetAuthPendingHook registers a callback fired when an OAuth authorization
// starts and the user must act in the browser.
func (m *Manager) SetAuthPendingHook(fn func(server, authURL string)) {
	m.onAuthPending = fn
}

// New creates an empty manager.
func New() *Manager {
	return &Manager{
		toolsets: map[string]tool.Toolset{},
		conns:    map[string]*trackedTransport{},
		states:   map[string]*ServerStatus{},
	}
}

// SetTokenStore enables OAuth token persistence (config-backed). Without it
// tokens live only for the process lifetime and consent re-runs per launch.
func (m *Manager) SetTokenStore(store *config.Store) {
	m.tokenStore = store
}

func (m *Manager) onAuthPendingWith(server string) func(authURL string) {
	return func(authURL string) {
		if m.onAuthPending != nil {
			m.onAuthPending(server, authURL)
		}
	}
}

func (m *Manager) buildTransportFor(cfg config.MCPServerConfig) (mcp.Transport, error) {
	return buildTransport(cfg, m.tokenStore, m.onAuthPendingWith(cfg.Name))
}

func buildTransport(cfg config.MCPServerConfig, tokenStore *config.Store, onAuthPending func(authURL string)) (mcp.Transport, error) {
	switch cfg.Transport {
	case config.MCPStreamableHTTP:
		if cfg.URL == "" {
			return nil, fmt.Errorf("streamable-http 传输需要填写 URL")
		}
		switch cfg.AuthType {
		case "oauth2":
			return newOAuthTransport(cfg, tokenStore, onAuthPending)
		case "bearer", "api-key":
			return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: authHTTPClient(cfg)}, nil
		}
		if len(cfg.Headers) > 0 {
			// Custom headers were configurable but historically unused; apply
			// them now via the transport's HTTP client.
			return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: headersOnlyClient(cfg)}, nil
		}
		return &mcp.StreamableClientTransport{Endpoint: cfg.URL}, nil
	default: // stdio
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio 传输需要填写启动命令")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if len(cfg.Env) > 0 {
			env := os.Environ()
			for k, v := range cfg.Env {
				env = append(env, k+"="+v)
			}
			cmd.Env = env
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	}
}

// Toolset returns (creating if needed) the toolset for a server.
// It does not connect; connection happens lazily on first tool use.
// Creation happens under the lock: two concurrent connects would otherwise
// build two transports and leak one of them (plus its stdio child).
func (m *Manager) Toolset(cfg config.MCPServerConfig) (tool.Toolset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts, ok := m.toolsets[cfg.ID]; ok {
		return ts, nil
	}
	tr, err := m.buildTransportFor(cfg)
	if err != nil {
		return nil, err
	}
	tracked := &trackedTransport{inner: tr}
	ts, err := mcptoolset.New(mcptoolset.Config{Transport: tracked})
	if err != nil {
		return nil, fmt.Errorf("创建 MCP 工具集「%s」失败: %w", cfg.Name, err)
	}
	m.toolsets[cfg.ID] = ts
	m.conns[cfg.ID] = tracked
	return ts, nil
}

// Connect verifies the server is reachable and caches its tool list.
func (m *Manager) Connect(cfg config.MCPServerConfig) ServerStatus {
	st := &ServerStatus{ID: cfg.ID, Name: cfg.Name, State: "connecting"}
	ts, err := m.Toolset(cfg)
	if err != nil {
		st.State = "error"
		st.Error = err.Error()
		m.setState(st)
		return *st
	}
	// First-time OAuth consent waits up to 5 minutes for the browser flow
	// (auth.go's fetcher cap); a 30s state-check timeout would always kill the
	// authorization before the user can finish logging in.
	limit := 30 * time.Second
	if cfg.AuthType == "oauth2" {
		limit = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	adkTools, err := ts.Tools(statusCtx{ctx})
	if err != nil {
		st.State = "error"
		st.Error = err.Error()
	} else {
		st.State = "connected"
		for _, t := range adkTools {
			st.Tools = append(st.Tools, ToolInfo{Name: t.Name(), Description: t.Description()})
		}
	}
	st.CheckedAt = time.Now()
	m.setState(st)
	return *st
}

// Disconnect drops the cached toolset for a server.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	tr := m.conns[id]
	delete(m.toolsets, id)
	delete(m.conns, id)
	if st, ok := m.states[id]; ok {
		st.State = "stopped"
		st.Tools = nil
		st.CheckedAt = time.Now()
	}
	m.mu.Unlock()
	// Close outside the lock: closing a stdio session waits on the child.
	if tr != nil {
		_ = tr.Close()
	}
}

// CloseAll disconnects every tracked server. The app shutdown hook calls it
// so stdio child processes do not outlive the desktop app as orphans.
// Idempotent: safe to call more than once.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

func (m *Manager) setState(st *ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[st.ID] = st
}

// Status returns the cached status for a server (or a stopped placeholder).
func (m *Manager) Status(id, name string) ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.states[id]; ok {
		return *st
	}
	return ServerStatus{ID: id, Name: name, State: "stopped"}
}

// AllStatus returns the statuses for the given configs.
func (m *Manager) AllStatus(cfgs []config.MCPServerConfig) []ServerStatus {
	out := make([]ServerStatus, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, m.Status(c.ID, c.Name))
	}
	return out
}

// statusCtx adapts a plain context to agent.ReadonlyContext for toolset
// queries that happen outside of a running invocation.
type statusCtx struct{ context.Context }

func (statusCtx) UserContent() *genai.Content          { return nil }
func (statusCtx) InvocationID() string                 { return "" }
func (statusCtx) AgentName() string                    { return "" }
func (statusCtx) ReadonlyState() session.ReadonlyState { return nil }
func (statusCtx) UserID() string                       { return "" }
func (statusCtx) AppName() string                      { return "" }
func (statusCtx) SessionID() string                    { return "" }
func (statusCtx) Branch() string                       { return "" }
