package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

var NewOpenAICompat = agentkit.NewOpenAICompat

func newTestRequest(text string) *model.LLMRequest {
	return &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}}
}

func newTestServices(store *config.Store) *Services {
	return &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
}

// TestStoreConcurrentUpdates hammers the config store from many goroutines to
// verify the mutex keeps state consistent (no race, no lost schema).
func TestStoreConcurrentUpdates(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.Update(func(cfg *config.Config) {
				cfg.Agents = append(cfg.Agents[:0], cfg.Agents...)
				cfg.Providers = append(cfg.Providers, config.ModelProvider{
					ID: "p", Name: "x", Protocol: config.ProtocolOpenAI,
				})
			})
		}(i)
	}
	wg.Wait()
	if got := len(store.Get().Providers); got != 32 {
		t.Fatalf("lost updates: got %d providers, want 32", got)
	}
}

// TestAdapterMalformedSSE feeds garbage lines, broken JSON and oversized
// payloads into both adapters: they must skip noise and still produce a final
// response without panicking.
func TestAdapterMalformedSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("not-an-event\n\n"))
		_, _ = w.Write([]byte("data: not-json{{{\n\n"))
		_, _ = w.Write([]byte("data: []\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: " + strings.Repeat("x", 300000) + "\n\n")) // oversized line
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm, err := agentkit.NewOpenAICompat(agentkit.OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	req := newTestRequest("hi")
	var text strings.Builder
	var usageSeen bool
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("malformed SSE produced error: %v", err)
		}
		if resp == nil {
			continue
		}
		if resp.Partial && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text.WriteString(p.Text)
			}
		}
		if !resp.Partial {
			usageSeen = resp.UsageMetadata != nil && resp.UsageMetadata.TotalTokenCount == 2
		}
	}
	if text.String() != "ok" {
		t.Fatalf("text = %q", text.String())
	}
	if !usageSeen {
		t.Fatal("usage chunk lost among malformed lines")
	}
}

// TestSendCancelIsStable verifies cancel during a slow run terminates cleanly
// without deadlocking the cancels map.
func TestSendCancelIsStable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	defer close(block)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Slow", Protocol: config.ProtocolOpenAI, BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	})
	db, sessions, err := newTestSessionService(dir + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	svc := newTestServices(store)
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = chat.Send(sess.ID, "slow", nil)
		close(done)
	}()
	for !chat.IsRunning(sess.ID) {
		select {
		case <-done:
			t.Fatal("send finished before cancel")
		default:
		}
	}
	chat.Cancel(sess.ID)
	<-done
	if chat.IsRunning(sess.ID) {
		t.Fatal("cancel left the run registered")
	}
	// Session remains usable afterwards.
	if _, err := chat.GetSessionMessages(sess.ID); err != nil {
		t.Fatalf("session unusable after cancel: %v", err)
	}
}
