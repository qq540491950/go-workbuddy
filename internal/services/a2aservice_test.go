package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestA2AServiceLoopback publishes a local agent over A2A, then connects to
// it with the app's own remote-agent client (loopback) and verifies a full
// conversation round trip, plus status transitions.
func TestA2AServiceLoopback(t *testing.T) {
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"本机 A2A 服务已响应"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("本机 A2A 服务已响应") + sseDone))
	}))
	defer modelSrv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: modelSrv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "本地服务助手", Description: "对外提供服务的 Agent", ProviderID: "p1", Model: "m",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	a2a := NewA2AService(svc, sessions)

	if sts := a2a.Status(); len(sts) != 0 {
		t.Fatalf("must start with no published agents: %+v", sts)
	}

	st, err := a2a.Start("a1", 0) // port 0: pick a free port
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.URL == "" || st.AgentName != agentkit.SanitizeAgentName("本地服务助手") {
		t.Fatalf("bad start status: %+v", st)
	}

	// Card discovery must resolve the published agent.
	info, err := agentkit.CheckRemoteAgent(t.Context(), st.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != st.AgentName || !info.Streaming {
		t.Fatalf("card mismatch: %+v vs %+v", info, st)
	}
	_ = info

	// Loopback conversation: a client ChatService talks to the published
	// agent through the full remote-agent client stack.
	clientDir := t.TempDir()
	clientStore, err := config.Open(clientDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientStore.Update(func(cfg *config.Config) {
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_loop", Name: "回环客户端", CardURL: st.URL,
		})
	}); err != nil {
		t.Fatal(err)
	}
	_, clientSessions, err := newTestSessionService(filepath.Join(clientDir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	clientSvc := &Services{Store: clientStore, Kit: agentkit.NewKit(clientStore, mcpmgr.New()), MCP: mcpmgr.New()}
	clientChat := NewChatService(clientSvc, clientSessions, db)
	sess, err := clientChat.NewSession("remote", "ra_loop", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientChat.Send(sess.ID, "在吗？", nil); err != nil {
		t.Fatal(err)
	}
	msgs, err := clientChat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var answered bool
	for _, m := range msgs {
		if m.Kind == "assistant" && strings.Contains(m.Text, "本机 A2A 服务已响应") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("loopback answer missing: %+v", msgs)
	}

	// Starting twice is idempotent for the same agent.
	if st2, err := a2a.Start("a1", 0); err != nil || !st2.Running || st2.URL != st.URL {
		t.Fatalf("double start should keep existing server: %+v err=%v", st2, err)
	}

	// StopAgent clears only that agent's entry.
	if err := a2a.StopAgent("a1"); err != nil {
		t.Fatal(err)
	}
	if sts := a2a.Status(); len(sts) != 0 {
		t.Fatalf("still published after StopAgent: %+v", sts)
	}
}
