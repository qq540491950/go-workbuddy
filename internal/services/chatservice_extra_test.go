package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// newChatFixture spins up a fake OpenAI SSE server whose reply is controlled
// by the caller, plus a chat service backed by a temp SQLite database.
func newChatFixture(t *testing.T, reply *atomic.Value) (*ChatService, config.ChatSession, *config.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		text, _ := reply.Load().(string)
		if text == "" {
			text = "first-reply"
		}
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = filepath.Join(dir, "workspace")
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []string{"fake-mini"},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_1", Name: "助手", ProviderID: "prov_1", Model: "fake-mini",
		})
	}); err != nil {
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
	return chat, sess, store
}

func TestRegenerateTrimsAndReruns(t *testing.T) {
	var reply atomic.Value
	reply.Store("first-reply")
	chat, sess, _ := newChatFixture(t, &reply)

	if err := chat.Send(sess.ID, "你好"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := chat.GetSessionMessages(sess.ID)
	if len(msgs) != 2 || msgs[1].Text != "first-reply" {
		t.Fatalf("pre-regenerate history = %+v", msgs)
	}

	reply.Store("second-reply")
	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatal(err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after regenerate, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != "user" || msgs[0].Text != "你好" {
		t.Fatalf("user message lost: %+v", msgs[0])
	}
	if msgs[1].Kind != "assistant" || msgs[1].Text != "second-reply" {
		t.Fatalf("expected regenerated reply, got %+v", msgs[1])
	}
}

func TestRegenerateWithoutUserMessage(t *testing.T) {
	var reply atomic.Value
	chat, sess, _ := newChatFixture(t, &reply)
	if err := chat.Regenerate(sess.ID); err == nil {
		t.Fatal("expected error regenerating empty session")
	}
}

func TestExportSessionWritesMarkdown(t *testing.T) {
	var reply atomic.Value
	reply.Store("导出测试回复 **加粗**")
	chat, sess, store := newChatFixture(t, &reply)
	if err := chat.Send(sess.ID, "导出我这段话"); err != nil {
		t.Fatal(err)
	}
	path, err := chat.ExportSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"# 导出我这段话", "## 🧑 用户", "导出我这段话", "## 🤖", "导出测试回复"} {
		if !strings.Contains(content, want) {
			t.Errorf("export missing %q\n%s", want, content)
		}
	}
	if !strings.Contains(path, filepath.Join(store.Get().Settings.WorkspaceDir, "exports")) {
		t.Errorf("export path outside workspace exports dir: %s", path)
	}
	if !strings.HasSuffix(path, ".md") {
		t.Errorf("export should be .md, got %s", path)
	}
	_ = context.Background
}

func TestSendAttachesUsageToEvents(t *testing.T) {
	var reply atomic.Value
	reply.Store("usage-reply")
	chat, sess, _ := newChatFixture(t, &reply)

	var events []ChatStreamEvent
	origApp := appEventHook
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = origApp })

	if err := chat.Send(sess.ID, "统计一下"); err != nil {
		t.Fatal(err)
	}
	var msgUsage, doneUsage *UsageInfo
	for _, ev := range events {
		if ev.Kind == "message" {
			msgUsage = ev.Usage
		}
		if ev.Kind == "done" {
			doneUsage = ev.SessionUsage
		}
	}
	if msgUsage == nil || msgUsage.TotalTokens != 14 {
		t.Fatalf("message usage = %+v", msgUsage)
	}
	if doneUsage == nil || doneUsage.TotalTokens != 14 || doneUsage.PromptTokens != 10 {
		t.Fatalf("done session usage = %+v", doneUsage)
	}
}
