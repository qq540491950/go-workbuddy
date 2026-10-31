package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestChatParallelSessions proves two sessions can run turns concurrently
// (per-session runner + cancel registry) without cross-talk: each session's
// history contains only its own conversation.
func TestChatParallelSessions(t *testing.T) {
	mkServer := func(reply string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sseText(reply) + sseDone))
		}))
	}
	srvA, srvB := mkServer("甲会话的回复"), mkServer("乙会话的回复")
	defer srvA.Close()
	defer srvB.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers,
			config.ModelProvider{ID: "pa", Name: "A", Protocol: config.ProtocolOpenAI, BaseURL: srvA.URL, Models: []config.ModelInfo{{ID: "m"}}},
			config.ModelProvider{ID: "pb", Name: "B", Protocol: config.ProtocolOpenAI, BaseURL: srvB.URL, Models: []config.ModelInfo{{ID: "m"}}},
		)
		cfg.Agents = append(cfg.Agents,
			config.AgentConfig{ID: "agent_a", Name: "甲", ProviderID: "pa", Model: "m"},
			config.AgentConfig{ID: "agent_b", Name: "乙", ProviderID: "pb", Model: "m"},
		)
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

	sessA, err := chat.newLegacySession("agent", "agent_a", "")
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := chat.newLegacySession("agent", "agent_b", "")
	if err != nil {
		t.Fatal(err)
	}

	// IsRunning must reflect in-flight state per session, independently.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := chat.Send(sessA.ID, "问甲", nil); err != nil {
			t.Errorf("session A send: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := chat.Send(sessB.ID, "问乙", nil); err != nil {
			t.Errorf("session B send: %v", err)
		}
	}()
	wg.Wait()

	historyOf := func(id string) []ChatMessage {
		t.Helper()
		msgs, err := chat.GetSessionMessages(id)
		if err != nil {
			t.Fatal(err)
		}
		return msgs
	}
	ma, mb := historyOf(sessA.ID), historyOf(sessB.ID)
	answerOf := func(msgs []ChatMessage) string {
		for _, m := range msgs {
			if m.Kind == "assistant" {
				return m.Text
			}
		}
		return ""
	}
	if !strings.Contains(answerOf(ma), "甲会话") {
		t.Fatalf("session A answer missing: %+v", ma)
	}
	if !strings.Contains(answerOf(mb), "乙会话") {
		t.Fatalf("session B answer missing: %+v", mb)
	}
	if chat.IsRunning(sessA.ID) || chat.IsRunning(sessB.ID) {
		t.Fatal("no session may report running after both turns complete")
	}
}
