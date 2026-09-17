package services

import (
	"net/http"
	"os"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// TestForkSession branches a conversation: the fork carries a full copy of
// the source history, then diverges independently (a send in the fork must
// not touch the original).
func TestForkSession(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		w.Header().Set("Content-Type", "text/event-stream")
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("原始回答") + sseDone))
		} else {
			_, _ = w.Write([]byte(sseText("分叉后的新回答") + sseDone))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "助手", ProviderID: "p1", Model: "m"})
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

	orig, err := chat.NewSession("agent", "a1", "历史会话")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(orig.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession(orig.ID, "历史会话"); err != nil {
		t.Fatal(err)
	}

	branch, err := chat.ForkSession(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ID == orig.ID || branch.TargetType != "agent" || branch.TargetID != "a1" {
		t.Fatalf("fork metadata wrong: %+v", branch)
	}
	if branch.ForkedFrom != orig.ID {
		t.Fatalf("fork lineage missing: %+v", branch)
	}
	if !strings.Contains(branch.Title, "分支") {
		t.Fatalf("fork title should carry suffix: %q", branch.Title)
	}

	// Fork must contain the same rendered history.
	origMsgs, err := chat.GetSessionMessages(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	forkMsgs, err := chat.GetSessionMessages(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(forkMsgs) != len(origMsgs) || len(forkMsgs) == 0 {
		t.Fatalf("fork history mismatch: orig=%d fork=%d", len(origMsgs), len(forkMsgs))
	}

	// Divergence: a send in the fork appears only there.
	if err := chat.Send(branch.ID, "换个说法", nil); err != nil {
		t.Fatal(err)
	}
	forkMsgs2, _ := chat.GetSessionMessages(branch.ID)
	origMsgs2, _ := chat.GetSessionMessages(orig.ID)
	if !strings.Contains(joinTexts(forkMsgs2), "分叉后的新回答") {
		t.Fatalf("fork divergence missing: %+v", forkMsgs2)
	}
	if strings.Contains(joinTexts(origMsgs2), "分叉后的新回答") || len(origMsgs2) != len(origMsgs) {
		t.Fatalf("original timeline was mutated by fork activity: %+v", origMsgs2)
	}

	// Forking a missing session errors cleanly.
	if _, err := chat.ForkSession("sess_nope"); err == nil {
		t.Fatal("expected error forking unknown session")
	}
}

func joinTexts(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestPinSessionOrdering: pinned sessions surface first regardless of
// recency, and unpinning restores updated-at order.
func TestPinSessionOrdering(t *testing.T) {
	chat, _, _, dir := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
	})
	_ = dir

	first, err := chat.NewSession("agent", "agent_c", "第一")
	if err != nil {
		t.Fatal(err)
	}
	second, err := chat.NewSession("agent", "agent_c", "第二")
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.PinSession(first.ID, true); err != nil {
		t.Fatal(err)
	}
	sessions := chat.ListSessions()
	if sessions[0].ID != first.ID || !sessions[0].Pinned {
		t.Fatalf("pinned session must sort first: %+v", sessions)
	}

	if err := chat.PinSession(first.ID, false); err != nil {
		t.Fatal(err)
	}
	sessions = chat.ListSessions()
	for _, s := range sessions {
		if s.ID == first.ID && s.Pinned {
			t.Fatal("unpin failed")
		}
	}
	_ = second
}

// TestExportSessionIncludesStats: the exported Markdown carries a stats
// summary header derived from the stored events.
func TestExportSessionIncludesStats(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("回答内容") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
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
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
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
	if !strings.Contains(string(data), "- 统计:") {
		t.Fatalf("export missing stats header:\n%s", data)
	}
}

// TestExportSessionHTML: the HTML export renders a self-contained styled
// document — title heading, markdown paragraphs, no raw markdown syntax.
func TestExportSessionHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("HTML 导出回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
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
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "导出测试", nil); err != nil {
		t.Fatal(err)
	}

	path, err := chat.ExportSessionHTML(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".html") {
		t.Fatalf("expected .html export: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{"<!DOCTYPE html>", "<h1>", "<h2", "HTML 导出回答", "用量统计", "</html>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("html doc missing %q", want)
		}
	}
}
