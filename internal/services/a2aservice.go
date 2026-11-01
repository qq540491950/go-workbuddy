package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/v2/runner"
	adka2av2 "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/session"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
)

// A2AService exposes the app's local agents over the A2A protocol so OTHER
// A2A clients (this app or third parties) can call them remotely. It mirrors
// the ADK launcher's server recipe: agent card discovery + JSON-RPC invoke.
type A2AService struct {
	S        *Services
	Sessions session.Service

	mu      sync.Mutex
	servers map[string]*a2aServer // agentID -> running server
}

type a2aServer struct {
	srv      *http.Server
	listener net.Listener
	agentID  string
	cardName string
	url      string
}

// NewA2AService creates the service. sessions backs the server-side stores of
// remote invocations (each remote call gets its own server-side session).
func NewA2AService(s *Services, sessions session.Service) *A2AService {
	return &A2AService{S: s, Sessions: sessions}
}

// A2AStatus describes one running published agent.
type A2AStatus struct {
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
}

// Start publishes the given local agent over A2A on the requested port
// (0 = pick a free one). Idempotent per agent: republishing a running agent
// returns its existing status. Multiple agents may be published at once.
func (a *A2AService) Start(agentID string, port int) (A2AStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.servers == nil {
		a.servers = map[string]*a2aServer{}
	}
	if existing, ok := a.servers[agentID]; ok {
		return A2AStatus{Running: true, URL: existing.url, AgentID: agentID, AgentName: existing.cardName}, nil
	}
	cfg := a.S.Store.Get()
	var agentCfg *config.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == agentID {
			agentCfg = &cfg.Agents[i]
			break
		}
	}
	if agentCfg == nil {
		return A2AStatus{}, fmt.Errorf("agent %q not found", agentID)
	}
	root, err := a.S.Kit.BuildAgent(*agentCfg)
	if err != nil {
		return A2AStatus{}, err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return A2AStatus{}, fmt.Errorf("端口监听失败: %w", err)
	}
	name := agentkit.SanitizeAgentName(agentCfg.Name)
	card := &a2acore.AgentCard{
		Name:               name,
		Description:        agentCfg.Description,
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             adka2av2.BuildAgentSkills(root),
		Capabilities:       a2acore.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2acore.AgentInterface{{
			URL:             "http://" + ln.Addr().String() + "/a2a/v1/invoke",
			ProtocolBinding: a2acore.TransportProtocolJSONRPC,
			ProtocolVersion: a2acore.Version,
		}},
	}
	executor := adka2av2.NewExecutor(adka2av2.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        name,
			Agent:          root,
			SessionService: a.Sessions,
		},
	})
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/a2a/v1/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	entry := &a2aServer{
		srv: srv, listener: ln, agentID: agentID,
		cardName: name, url: "http://" + ln.Addr().String(),
	}
	// Register before serving so the failure path below can find the entry.
	a.servers[agentID] = entry
	go func() {
		// Serve only returns on listener failure or shutdown; either way the
		// agent is no longer reachable, so drop it instead of reporting a
		// running server that refuses connections.
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.mu.Lock()
			if cur, ok := a.servers[agentID]; ok && cur.srv == srv {
				delete(a.servers, agentID)
			}
			a.mu.Unlock()
		}
	}()
	return A2AStatus{Running: true, URL: entry.url, AgentID: agentID, AgentName: name}, nil
}

// StopPublishedAgent is a best-effort hook for config mutations: if the given
// agent is currently published over A2A, its server is stopped so remote
// clients cannot keep invoking a stale (edited) or deleted agent tree. Unknown
// IDs are a no-op.
func (s *Services) StopPublishedAgent(agentID string) {
	if s.A2A == nil {
		return
	}
	_ = s.A2A.StopAgent(agentID)
}

// StopAgent shuts down one published agent's server.
func (a *A2AService) StopAgent(agentID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.servers[agentID]
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := entry.srv.Shutdown(ctx)
	if err != nil {
		// Graceful shutdown timed out with streaming calls still in flight:
		// force-close so status matches reality (the entry is deleted below)
		// and lingering handlers stop writing to the session store.
		_ = entry.srv.Close()
	}
	delete(a.servers, agentID)
	return err
}

// StopAll shuts down every published agent server.
func (a *A2AService) StopAll() {
	a.mu.Lock()
	ids := make([]string, 0, len(a.servers))
	for id := range a.servers {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		_ = a.StopAgent(id)
	}
}

// Status lists all currently published agents.
func (a *A2AService) Status() []A2AStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]A2AStatus, 0, len(a.servers))
	for _, e := range a.servers {
		out = append(out, A2AStatus{
			Running: true, URL: e.url, AgentID: e.agentID, AgentName: e.cardName,
		})
	}
	return out
}
