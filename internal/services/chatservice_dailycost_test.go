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

// TestSessionStatsDailyCost verifies the per-day token breakdown and the
// cost estimate derived from the target model's configured prices.
func TestSessionStatsDailyCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":1000000,"completion_tokens":500000,"total_tokens":1500000}}` + "\n\n"))
		_, _ = w.Write([]byte(sseText("统计回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Price: ¥2/1M input, ¥8/1M output.
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL,
			Models:  []config.ModelInfo{{ID: "m", PriceIn: 2, PriceOut: 8}},
			IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
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

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "统计我", nil); err != nil {
		t.Fatal(err)
	}

	st, err := chat.SessionStats(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.TokensIn != 1000000 || st.TokensOut != 500000 {
		t.Fatalf("totals wrong: %+v", st)
	}
	if len(st.Daily) != 1 {
		t.Fatalf("expected one day entry, got %+v", st.Daily)
	}
	d := st.Daily[0]
	if len(d.Date) != 10 || strings.Count(d.Date, "-") != 2 {
		t.Fatalf("date format wrong: %q", d.Date)
	}
	if d.PromptTokens != 1000000 || d.CompletionTokens != 500000 {
		t.Fatalf("day tokens wrong: %+v", d)
	}
	// Per-author breakdown: single assistant author owns all tokens.
	if len(st.Authors) != 1 || st.Authors[0].Author != "a" || st.Authors[0].Tokens != 1500000 || st.Authors[0].Turns != 1 {
		t.Fatalf("author stats wrong: %+v", st.Authors)
	}
	wantCost := float64(1000000)/1e6*2 + float64(500000)/1e6*8 // 2 + 4 = 6
	if diff := d.EstimatedCost - wantCost; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("cost wrong: got %v want %v", d.EstimatedCost, wantCost)
	}
}
