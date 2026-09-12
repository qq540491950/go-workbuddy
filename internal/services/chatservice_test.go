package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestChatEndToEndToolLoop exercises the full pipeline: ChatService.Send ->
// ADK runner -> llmagent -> custom OpenAI-compatible model adapter (against a
// fake SSE server) -> function-tool loop -> SQLite session persistence.
func TestChatEndToEndToolLoop(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_current_time","arguments":"{}"}}]}}]}` + "\n\n"))
		} else {
			body := readAll(r)
			if !strings.Contains(body, `"tool"`) {
				t.Errorf("expected function response message in turn 2, got: %s", body)
			}
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"现在时间是上午，任务完成。"}}]}` + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []string{"fake-mini"}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_1", Name: "时间助手", ProviderID: "prov_1", Model: "fake-mini",
			SystemPrompt: "你负责查询时间", Temperature: 0.3, MaxTokens: 512,
			BuiltinTools: []string{"time"},
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
	chat := NewChatService(svc, sessions, db)

	sess, err := chat.NewSession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "现在几点了？"); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	var sawUser, sawToolCall, sawToolResult, sawAnswer bool
	for _, m := range msgs {
		switch {
		case m.Kind == "user" && strings.Contains(m.Text, "几点"):
			sawUser = true
		case m.Kind == "tool_call" && m.ToolName == "get_current_time":
			sawToolCall = true
		case m.Kind == "tool_result" && m.ToolName == "get_current_time":
			sawToolResult = true
		case m.Kind == "assistant" && strings.Contains(m.Text, "任务完成"):
			sawAnswer = true
		}
	}
	if !sawUser || !sawToolCall || !sawToolResult || !sawAnswer {
		t.Fatalf("pipeline incomplete: user=%v toolCall=%v toolResult=%v answer=%v\nmessages: %+v",
			sawUser, sawToolCall, sawToolResult, sawAnswer, msgs)
	}

	// Session title auto-set from first message.
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && s.Title == "新对话" {
			t.Fatal("expected session title to be set from first message")
		}
	}

	// History is persisted: a fresh service reads the same session.
	chat2 := NewChatService(svc, sessions, db)
	msgs2, err := chat2.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != len(msgs) {
		t.Fatalf("persisted history mismatch: %d vs %d", len(msgs2), len(msgs))
	}
}

func readAll(r *http.Request) string {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func newTestSessionService(path string) (*gorm.DB, session.Service, error) {
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		return nil, nil, err
	}
	svc, err := database.NewSessionServiceFromDB(db)
	if err != nil {
		return nil, nil, err
	}
	if err := database.AutoMigrate(svc); err != nil {
		return nil, nil, err
	}
	return db, svc, nil
}
