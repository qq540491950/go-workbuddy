package services

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	adka2av2 "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"changeme/internal/agentkit"
	"changeme/internal/config"
)

// A2AService exposes the app's local agents over the A2A protocol so OTHER
// A2A clients (this app or third parties) can call them remotely. It mirrors
// the ADK launcher's server recipe: agent card discovery + JSON-RPC invoke.
type A2AService struct {
	S        *Services
	Sessions session.Service

	mu       sync.Mutex
	srv      *http.Server
	listener net.Listener
	agentID  string
	cardName string
}

// NewA2AService creates the service. sessions backs the server-side stores of
// remote invocations (each remote call gets its own server-side session).
func NewA2AService(s *Services, sessions session.Service) *A2AService {
	return &A2AService{S: s, Sessions: sessions}
}

// A2AStatus describes the current server state for the UI.
type A2AStatus struct {
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
}

// Start publishes the given local agent over A2A on the requested port
// (0 = pick a free one) and returns the status with the advertised card URL.
func (a *A2AService) Start(agentID string, port int) (A2AStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		return a.currentStatus(), nil
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
	go func() {
		_ = srv.Serve(ln)
	}()
	a.srv = srv
	a.listener = ln
	a.agentID = agentID
	a.cardName = name
	return a.currentStatus(), nil
}

// Stop shuts the A2A server down.
func (a *A2AService) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := a.srv.Shutdown(ctx)
	a.srv = nil
	a.listener = nil
	a.agentID = ""
	a.cardName = ""
	return err
}

// Status reports whether the server is running.
func (a *A2AService) Status() A2AStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentStatus()
}

func (a *A2AService) currentStatus() A2AStatus {
	if a.srv == nil || a.listener == nil {
		return A2AStatus{}
	}
	return A2AStatus{
		Running:   true,
		URL:       "http://" + a.listener.Addr().String(),
		AgentID:   a.agentID,
		AgentName: a.cardName,
	}
}
