package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

// newDirectChat wires a ChatService against a fake model server with ONLY a
// default provider (no agents), so tests can exercise target-less direct
// sessions and @mention routing.
func newDirectChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, readAll(r))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_d", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store, &bodies
}

// TestDirectChatWithoutAgent pins the zero-config chat contract: a session
// with no target runs on the default provider's model and never mutates the
// session binding.
func TestDirectChatWithoutAgent(t *testing.T) {
	chat, store, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("你好！") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "在吗", nil); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "在吗") {
		t.Fatalf("model did not receive the message: %v", *bodies)
	}
	var sawUser, sawReply bool
	for _, ev := range *events {
		if ev.Kind == "user" && ev.Text == "在吗" {
			sawUser = true
		}
		if ev.Kind == "message" && ev.Author == "助手" && ev.Text == "你好！" {
			sawReply = true
		}
	}
	if !sawUser || !sawReply {
		t.Fatalf("missing stream events: user=%v reply=%v", sawUser, sawReply)
	}
	cfg := store.Get()
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sess.ID && cfg.Sessions[i].TargetType != "" {
			t.Fatalf("direct session target mutated: %+v", cfg.Sessions[i])
		}
	}
}

// TestMentionRoutesTurnToAgent pins @mention routing: a leading "@名称" sends
// that one turn to the named agent while the session binding stays direct,
// and Regenerate re-runs the stored mention with the same routing.
func TestMentionRoutesTurnToAgent(t *testing.T) {
	chat, store, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("收到") + sseDone))
	})
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_w", Name: "写手助手", ProviderID: "prov_d", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "@写手助手 帮我起草周报", nil); err != nil {
		t.Fatal(err)
	}
	author := ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "写手助手" {
		t.Fatalf("mention did not route the turn, author=%q", author)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "帮我起草周报") {
		t.Fatalf("mentioned agent missing the message text: %v", *bodies)
	}
	cfg := store.Get()
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sess.ID && (cfg.Sessions[i].TargetType != "" || cfg.Sessions[i].TargetID != "") {
			t.Fatalf("mention mutated the session binding: %+v", cfg.Sessions[i])
		}
	}

	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatal(err)
	}
	author = ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "写手助手" {
		t.Fatalf("regenerate lost the mention routing, author=%q", author)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
}

// TestMentionUnknownFallsBackToDirect: an @name matching nothing runs the
// turn as a plain direct chat instead of failing.
func TestMentionUnknownFallsBackToDirect(t *testing.T) {
	chat, _, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好的") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "@不存在的人 你好", nil); err != nil {
		t.Fatal(err)
	}
	author := ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "助手" {
		t.Fatalf("unknown mention should fall back to direct chat, author=%q", author)
	}
}

// TestDirectChatWithoutProviderFails: with no provider configured the turn
// is refused with an actionable message instead of a generic error.
func TestDirectChatWithoutProviderFails(t *testing.T) {
	chat, store, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("model must not be called without a provider")
	})
	if err := store.Update(func(cfg *config.Config) { cfg.Providers = nil }); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = chat.Send(sess.ID, "你好", nil)
	if err == nil || !strings.Contains(err.Error(), "模型配置") {
		t.Fatalf("expected provider guidance error, got %v", err)
	}
}
