package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/v2/session"

	"changeme/internal/agentkit"
	"changeme/internal/config"
)

// A panicking or failing agent runtime must never take the app down: the
// session stays usable after a recovered turn.
func TestSendTurnKeepsSessionUsableAfterFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(dir + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, nil), MCP: nil}
	chat := NewChatService(svc, sessions, db)

	var events []ChatStreamEvent
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })

	sess, err := chat.NewSession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	// Unreachable provider: the turn fails but the app must stay healthy.
	_ = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "X", Protocol: config.ProtocolOpenAI, BaseURL: "http://127.0.0.1:1", Models: []string{"m"},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "A", ProviderID: "p1", Model: "m"})
	})

	_ = chat.Send(sess.ID, "hello", nil)
	if _, err := chat.GetSessionMessages(sess.ID); err != nil {
		t.Fatalf("session unusable after failed turn: %v", err)
	}
	// An error event was surfaced to the UI.
	if len(events) == 0 {
		t.Fatal("expected stream events for the failed turn")
	}
}

// A corrupted sessions database is backed up and recreated transparently.
func TestCorruptSessionDBSelfHeals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database at all, just noise bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, svc, err := openSessionServiceImpl(path)
	if err != nil {
		t.Fatalf("self-heal failed: %v", err)
	}
	if _, err := svc.Create(context.Background(), &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatalf("recreated db unusable: %v", err)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("expected one corrupt backup, got %v", matches)
	}
	_ = db
}

// config.json must be 0600 since it holds API keys.
func TestConfigFilePermissionRestricted(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{ID: "p", Name: "n"})
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(store.Dir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config.json perms = %o, want 600", perm)
	}
}
