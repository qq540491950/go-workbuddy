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
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)

	orig, err := chat.newLegacySession("agent", "a1", "历史会话")
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

	first, err := chat.newLegacySession("agent", "agent_c", "第一")
	if err != nil {
		t.Fatal(err)
	}
	second, err := chat.newLegacySession("agent", "agent_c", "第二")
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

// TestSessionPreviewUpdated: after a turn the session record carries the
// latest assistant reply tail for the sidebar.
func TestSessionPreviewUpdated(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("第一版回答") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("第二版回答") + sseDone))
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
	if err := chat.Send(sess.ID, "问题一", nil); err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID {
			found = s.Preview
		}
	}
	if !strings.Contains(found, "第一版回答") {
		t.Fatalf("preview missing after turn: %q", found)
	}
	if err := chat.Send(sess.ID, "问题二", nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && !strings.Contains(s.Preview, "第二版回答") {
			t.Fatalf("preview not refreshed: %q", s.Preview)
		}
	}
}

// TestExportSessionMarksForkLine: forking from a session that already has a
// history makes the exported Markdown note the origin.
func TestExportSessionMarksForkLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("回答") + sseDone))
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
	if err := chat.Send(sess.ID, "原始", nil); err != nil {
		t.Fatal(err)
	}
	branch, err := chat.ForkSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := chat.ExportSession(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "分叉自") {
		t.Fatalf("fork origin missing in export:\n%s", data)
	}
}

// TestSessionLifecycleAudit: create/fork/delete/rename all land in the JSONL
// audit trail when enabled.
func TestSessionLifecycleAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
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
		cfg.Settings.ToolsAudit = true
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	chat.SetToolAuditPath(auditPath)

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession(sess.ID, "改名"); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.ForkSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, phase := range []string{"session_created", "session_renamed", "session_forked", "session_deleted"} {
		if !strings.Contains(doc, phase) {
			t.Fatalf("audit missing %s:\n%s", phase, doc)
		}
	}
}

// TestArchiveSessionRoundTrip: archive/unarchive toggles the persisted flag
// and writes both phases to the audit trail.
func TestArchiveSessionRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
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
	auditPath := filepath.Join(dir, "audit.jsonl")
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(sess.ID, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID {
			found = s.Archived
		}
	}
	if !found {
		t.Fatal("archived flag not persisted")
	}
	if err := chat.ArchiveSession(sess.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && s.Archived {
			t.Fatal("unarchive failed")
		}
	}
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), "session_archived") || !strings.Contains(string(audit), "session_unarchived") {
		t.Fatalf("archive phases missing from audit:\n%s", audit)
	}
}

// TestExportAllSessions: bulk export writes one Markdown file per session.
func TestExportAllSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("批量导出回答") + sseDone))
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
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	s1, err := chat.newLegacySession("agent", "a1", "会话一")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := chat.newLegacySession("agent", "a1", "会话二")
	if err != nil {
		t.Fatal(err)
	}
	_ = s2
	if err := chat.Send(s1.ID, "内容", nil); err != nil {
		t.Fatal(err)
	}

	n, err := chat.ExportAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("expected at least 2 exported sessions, got %d", n)
	}
}
