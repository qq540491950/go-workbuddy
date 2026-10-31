package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestAutoTitleGeneratesTitle: after the first exchange the model is asked for
// a short title (a non-streaming side call) and the truncated-text fallback is
// replaced. Failure of the side call must keep the fallback.
func TestAutoTitleGeneratesTitle(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllBody(r)
		bodies = append(bodies, body)
		if strings.Contains(body, `"stream":false`) {
			// Title request: plain completion.
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"周报规划讨论"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好的，我们来规划。") + sseDone))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, dir + "/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_at", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}, {ID: "fake-large"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_at", Name: "助手", ProviderID: "prov_at", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	// Auto-title is opt-in by default: enable it for this test.
	on := true
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.AutoTitle = &on
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_at", "")
	if err != nil {
		t.Fatal(err)
	}
	// Pin the session to the overridden model: the title side call must use
	// the same effective model as the conversation (EffectiveModel clamp).
	if err := chat.SetSessionModel(sess.ID, "fake-large"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "帮我规划写周报的完整流程", nil); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		title := ""
		for _, s := range chat.S.Store.Get().Sessions {
			if s.ID == sess.ID {
				title = s.Title
			}
		}
		if title == "周报规划讨论" {
			// The title request went through the session's model.
			found := false
			for _, b := range bodies {
				if strings.Contains(b, "中文标题") {
					if !strings.Contains(b, `"model":"fake-large"`) {
						t.Fatalf("title side call ignored the session model override: %s", b)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("title generated without the model prompt")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("title not generated; last=%q", title)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAutoTitleDisabledKeepsFallback: turning the setting off (or a failing
// side call) keeps the truncated first-message fallback.
func TestAutoTitleDisabledKeepsFallback(t *testing.T) {
	chat, store, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	hookEvents(t)
	off := false
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.AutoTitle = &off
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "帮我规划写周报的完整流程这条消息足够长会被截断", nil); err != nil {
		t.Fatal(err)
	}
	title := ""
	for _, s := range chat.S.Store.Get().Sessions {
		if s.ID == sess.ID {
			title = s.Title
		}
	}
	if title == "" || len([]rune(title)) > 30 {
		t.Fatalf("fallback title unexpected: %q", title)
	}
}
