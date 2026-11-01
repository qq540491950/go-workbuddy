package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/v2/agent/llmagent"
	adka2av2 "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/runner"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

// TestTeamRemoteMemberE2E builds a team whose only member is a remote A2A
// agent; the auto coordinator (fake model) transfers to it and the remote
// answer streams back through the local session history.
func TestTeamRemoteMemberE2E(t *testing.T) {
	// Remote model behind the A2A server.
	remoteModelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"远程成员的回复"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("远程成员的回复") + sseDone))
	}))
	defer remoteModelSrv.Close()

	remoteModel, err := agentkit.NewOpenAICompat(agentkit.OpenAICompatConfig{BaseURL: remoteModelSrv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := llmagent.New(llmagent.Config{Name: "remote_helper", Model: remoteModel})
	if err != nil {
		t.Fatal(err)
	}
	_, serverSessions, err := newTestSessionService(t, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	a2aMux := http.NewServeMux()
	a2aSrv := httptest.NewServer(a2aMux)
	defer a2aSrv.Close()
	card := &a2acore.AgentCard{
		Name: "remote_helper", Version: "1.0.0",
		DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2acore.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2acore.AgentInterface{{
			URL:             a2aSrv.URL + "/a2a/v1/invoke",
			ProtocolBinding: a2acore.TransportProtocolJSONRPC,
			ProtocolVersion: a2acore.Version,
		}},
	}
	executor := adka2av2.NewExecutor(adka2av2.ExecutorConfig{
		RunnerConfig: runner.Config{AppName: "remote_helper", Agent: hosted, SessionService: serverSessions},
	})
	a2aMux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	a2aMux.Handle("/a2a/v1/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	// Coordinator model: transfer to the remote member on the first turn.
	coordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
			target := agentkit.SanitizeAgentName("远程助手")
		args := `"{\"agent_name\":\"` + target + `\"}"`
		_, _ = w.Write([]byte(sseToolCall("t1", "transfer_to_agent", args) + sseDone))
	}))
	defer coordSrv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "pc", Name: "Coord", Protocol: config.ProtocolOpenAI,
			BaseURL: coordSrv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_1", Name: "远程助手", CardURL: a2aSrv.URL,
		})
		cfg.Teams = append(cfg.Teams, config.TeamConfig{
			ID: "team_r", Name: "混合团队", ProviderID: "pc", Model: "m",
			AutoCoordinate:  true,
			RemoteMemberIDs: []string{"ra_1"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)

	sess, err := chat.newLegacySession("team", "team_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "请远程的伙伴帮忙", nil); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawRemoteAnswer, sawTransfer bool
	for _, m := range msgs {
		switch {
		case m.Kind == "assistant" && strings.Contains(m.Text, "远程成员的回复"):
			sawRemoteAnswer = true
		case m.Kind == "tool_call" && m.ToolName == "transfer_to_agent":
			sawTransfer = true
		}
	}
	if !sawRemoteAnswer {
		t.Fatalf("remote member answer missing from team history: %+v", msgs)
	}
	if !sawTransfer {
		t.Fatalf("transfer call missing from team history: %+v", msgs)
	}
}
