package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

func newProjectTestChat(t *testing.T) (*ChatService, *config.Store, string) {
	t.Helper()
	return newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
}

func findStoredSession(t *testing.T, store *config.Store, id string) config.ChatSession {
	t.Helper()
	for _, s := range store.Get().Sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %q missing", id)
	return config.ChatSession{}
}

func TestSaveProjectValidatesAndDedupes(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := t.TempDir()

	p, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != filepath.Base(root) {
		t.Fatalf("default name = %q, want folder name %q", p.Name, filepath.Base(root))
	}
	if _, err := chat.SaveProject("", "另一个项目", root); err == nil || !strings.Contains(err.Error(), "已被项目") {
		t.Fatalf("expected duplicate-root rejection, got %v", err)
	}
	if _, err := chat.SaveProject("", "", filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected missing dir rejection")
	}
	// Rename keeps the root; a blank root on update is a no-op.
	renamed, err := chat.SaveProject(p.ID, "go-workbuddy", "")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "go-workbuddy" || renamed.RootDir != p.RootDir {
		t.Fatalf("rename changed root: %+v", renamed)
	}
}

func TestNewSessionWithProject(t *testing.T) {
	chat, store, global := newProjectTestChat(t)
	root := t.TempDir()
	p, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := chat.NewSessionWithProject("", "", "项目对话", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := findStoredSession(t, store, sess.ID)
	if stored.ProjectID != p.ID {
		t.Fatalf("ProjectID = %q, want %q", stored.ProjectID, p.ID)
	}
	if stored.WorkspaceDir != "" {
		t.Fatalf("project sessions must not own a task dir, got %q", stored.WorkspaceDir)
	}
	if _, err := os.Stat(filepath.Join(global, "tasks", sess.ID)); !os.IsNotExist(err) {
		t.Fatalf("auto task dir must not be created for project sessions: %v", err)
	}
	ws, err := chat.SessionWorkspace(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws != p.RootDir {
		t.Fatalf("SessionWorkspace = %q, want project root %q", ws, p.RootDir)
	}
	if _, err := chat.NewSessionWithProject("", "", "幽灵", "proj_missing"); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("expected unknown-project rejection, got %v", err)
	}
}

func TestSetSessionProjectBeforeFirstTurnOnly(t *testing.T) {
	chat, store, _ := newProjectTestChat(t)
	p, err := chat.SaveProject("", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithWorkspace("", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionProject(sess.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if stored := findStoredSession(t, store, sess.ID); stored.ProjectID != p.ID || stored.WorkspaceDir != "" {
		t.Fatalf("join project must take over the workspace: %+v", stored)
	}
	ws, err := chat.SessionWorkspace(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws != p.RootDir {
		t.Fatalf("SessionWorkspace = %q, want %q", ws, p.RootDir)
	}
	if err := chat.Send(sess.ID, "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionProject(sess.ID, ""); err == nil || !strings.Contains(err.Error(), "首轮") {
		t.Fatalf("expected post-first-turn lock, got %v", err)
	}
}

func TestDeleteProjectFallsBackSessions(t *testing.T) {
	chat, store, global := newProjectTestChat(t)
	p, err := chat.SaveProject("", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithProject("", "", "待回落", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if stored := findStoredSession(t, store, sess.ID); stored.ProjectID != "" {
		t.Fatalf("ProjectID = %q, want empty after project deletion", stored.ProjectID)
	}
	ws, err := chat.SessionWorkspace(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws != filepath.Clean(global) {
		t.Fatalf("SessionWorkspace = %q, want global %q", ws, filepath.Clean(global))
	}
	if got := chat.ListProjects(); len(got) != 0 {
		t.Fatalf("ListProjects after delete = %+v", got)
	}
}

func TestProjectRootRejectedAsTaskWorkspace(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := t.TempDir()
	if _, err := chat.SaveProject("", "", root); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "占目录", root); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("expected project-root rejection on create, got %v", err)
	}
	sess, err := chat.NewSessionWithWorkspace("", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionWorkspace(sess.ID, root); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("expected project-root rejection on rebind, got %v", err)
	}
}

func TestForkSessionKeepsProject(t *testing.T) {
	chat, store, _ := newProjectTestChat(t)
	p, err := chat.SaveProject("", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithProject("agent", "agent_ws", "源", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "hello", nil); err != nil {
		t.Fatal(err)
	}
	branch, err := chat.ForkSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := findStoredSession(t, store, branch.ID)
	if stored.ProjectID != p.ID {
		t.Fatalf("branch ProjectID = %q, want %q", stored.ProjectID, p.ID)
	}
	if stored.WorkspaceDir != "" {
		t.Fatalf("branch must stay a project session, got WorkspaceDir %q", stored.WorkspaceDir)
	}
	ws, err := chat.SessionWorkspace(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws != p.RootDir {
		t.Fatalf("branch workspace = %q, want %q", ws, p.RootDir)
	}
}
