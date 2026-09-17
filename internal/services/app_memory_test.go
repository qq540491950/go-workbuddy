package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/memory"
	"changeme/internal/mcpmgr"
)

// TestMemoryStatsAndClear: the remember flow feeds the store, stats report
// the entries, and clear wipes them.
func TestMemoryStatsAndClear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("记住这条事实") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	memSvc, err := memory.NewSQLiteService(db)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New(), Memory: memSvc}
	app := NewAppService(svc)
	chat := NewChatService(svc, sessions, db)

	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "记住内容", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.RememberSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	info, err := app.MemoryStats()
	if err != nil {
		t.Fatal(err)
	}
	if info.Count <= 0 {
		t.Fatalf("expected memory entries, got %+v", info)
	}
	if err := app.MemoryClear(); err != nil {
		t.Fatal(err)
	}
	info, err = app.MemoryStats()
	if err != nil {
		t.Fatal(err)
	}
	if info.Count != 0 {
		t.Fatalf("memory not cleared: %+v", info)
	}
	if !strings.Contains("记忆服务不可用", "记忆服务") {
		t.Fatal("sanity")
	}
}
