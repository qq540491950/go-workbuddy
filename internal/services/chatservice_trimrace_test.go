package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// slowTurnServer streams the first turn in several delayed chunks (the run is
// genuinely in-flight while the test pokes it) and answers any later turn
// with a single fast replacement reply.
func slowTurnServer(t *testing.T, fastReply string, firstDelta chan struct{}) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn > 1 {
			_, _ = w.Write([]byte(sseText(fastReply) + sseDone))
			return
		}
		opened := false
		for i, chunk := range []string{"甲", "乙", "丙", "丁"} {
			if i == 1 && !opened {
				opened = true
				close(firstDelta)
			}
			_, _ = w.Write([]byte(sseText(chunk)))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(150 * time.Millisecond)
		}
		_, _ = w.Write([]byte(sseDone))
	}))
}

// newTrimRaceChat wires a one-agent store + chat service against srv.
func newTrimRaceChat(t *testing.T, srv *httptest.Server) (*ChatService, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "pv", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "甲", ProviderID: "pv", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.NewSession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	return chat, sess.ID
}

// TestRegenerateWaitsForOldRun pins the invariant: Regenerate must cancel the
// in-flight turn AND wait until it has fully unwound before trimming history.
// An old run that keeps streaming past the trim would resurrect deleted
// events (old reply text appearing in the regenerated timeline).
func TestRegenerateWaitsForOldRun(t *testing.T) {
	firstDelta := make(chan struct{})
	srv := slowTurnServer(t, "重答内容：精简版答案", firstDelta)
	defer srv.Close()
	chat, sessID := newTrimRaceChat(t, srv)

	oldDone := make(chan error, 1)
	go func() { oldDone <- chat.Send(sessID, "第一问", nil) }()
	<-firstDelta

	if err := chat.Regenerate(sessID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if err := <-oldDone; err == nil {
		t.Log("old turn finished with nil error (it had already completed)")
	}

	if chat.IsRunning(sessID) {
		t.Fatal("session still running after Regenerate returned")
	}
	msgs, err := chat.GetSessionMessages(sessID)
	if err != nil {
		t.Fatal(err)
	}
	var users, assistants int
	var answer strings.Builder
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			users++
		case "assistant":
			assistants++
			answer.WriteString(m.Text)
		}
	}
	if users != 1 || assistants != 1 {
		t.Fatalf("regenerated timeline must hold exactly one user turn and one reply, got users=%d assistants=%d: %+v", users, assistants, msgs)
	}
	if !strings.Contains(answer.String(), "重答内容") {
		t.Fatalf("new reply missing: %q", answer.String())
	}
	if strings.Contains(answer.String(), "甲乙丙丁") {
		t.Fatal("trimmed old turn resurrected after Regenerate")
	}
}

// TestDeleteSessionWaitsForOldRun pins the same contract for DeleteSession:
// after it returns, the in-flight run must be gone and no event rows may be
// written for the deleted session afterwards.
func TestDeleteSessionWaitsForOldRun(t *testing.T) {
	firstDelta := make(chan struct{})
	srv := slowTurnServer(t, "无关", firstDelta)
	defer srv.Close()
	chat, sessID := newTrimRaceChat(t, srv)
	db := chat.db

	oldDone := make(chan error, 1)
	go func() { oldDone <- chat.Send(sessID, "第一问", nil) }()
	<-firstDelta

	if err := chat.DeleteSession(sessID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if err := <-oldDone; err == nil {
		t.Log("old turn finished with nil error (it had already completed)")
	}

	var n int64
	if err := db.Raw("SELECT count(*) FROM events WHERE session_id = ?", sessID).Row().Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("deleted session has %d event rows (orphaned or resurrected)", n)
	}
}
