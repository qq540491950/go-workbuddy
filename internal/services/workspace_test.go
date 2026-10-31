package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"

	"google.golang.org/adk/v2/session"
)

// newLegacySession keeps pre-task-workspace tests on their original global
// fallback without exposing a test-only constructor through Wails.
func (c *ChatService) newLegacySession(targetType, targetID, title string) (config.ChatSession, error) {
	if title == "" {
		title = "新对话"
	}
	id := newID("sess_")
	cs := config.ChatSession{
		ID: id, Title: title, TargetType: targetType, TargetID: targetID,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		return config.ChatSession{}, err
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{cs}, cfg.Sessions...)
	})
	if err == nil {
		c.audit(cs.ID, "session_created", "system", targetType, mustJSON(map[string]string{"targetId": targetID, "title": cs.Title}), "")
	}
	return cs, err
}

func newWorkspaceTestChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_ws", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_ws", Name: "文件助手", ProviderID: "prov_ws", Model: "fake-mini",
			SystemPrompt: "workspace={{workspace}}", BuiltinTools: []string{"files"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store, workspace
}

func TestNewSessionWorkspaceIsolatesParallelWrites(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	chat, _, globalWorkspace := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body, `\"ok\":true`) {
			_, _ = w.Write([]byte(sseText("written") + sseDone))
			return
		}
		label := "A"
		if strings.Contains(body, "write-b") {
			label = "B"
		}
		args := fmt.Sprintf(`"{\"path\":\"same.txt\",\"content\":\"%s\"}"`, label)
		_, _ = w.Write([]byte(sseToolCall("call_"+label, "write_workspace_file", args) + sseDone))
	})

	a, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.WorkspaceDir == "" || b.WorkspaceDir == "" {
		t.Fatalf("new sessions must persist task workspaces: A=%q B=%q", a.WorkspaceDir, b.WorkspaceDir)
	}
	if a.WorkspaceDir == b.WorkspaceDir {
		t.Fatalf("parallel sessions share a workspace: %q", a.WorkspaceDir)
	}
	if !strings.HasPrefix(filepath.Clean(a.WorkspaceDir), filepath.Clean(globalWorkspace)+string(os.PathSeparator)) {
		t.Fatalf("auto workspace %q must live under global workspace %q", a.WorkspaceDir, globalWorkspace)
	}

	if err := chat.Send(a.ID, "write-a", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(b.ID, "write-b", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		session config.ChatSession
		want    string
	}{{a, "A"}, {b, "B"}} {
		got, err := os.ReadFile(filepath.Join(tc.session.WorkspaceDir, "same.txt"))
		if err != nil {
			t.Fatalf("read %s: %v", tc.session.ID, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s wrote %q, want %q", tc.session.ID, got, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(globalWorkspace, "same.txt")); !os.IsNotExist(err) {
		t.Fatalf("task write leaked into the global workspace: %v", err)
	}
	var first struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if len(requests) == 0 {
		t.Fatal("model server saw no request")
	}
	if err := json.Unmarshal([]byte(requests[0]), &first); err != nil {
		t.Fatalf("decode model request: %v", err)
	}
	if len(first.Messages) == 0 || !strings.Contains(first.Messages[0].Content, a.WorkspaceDir) {
		t.Fatalf("prompt workspace variable did not resolve to the task directory: %q", requests[0])
	}
}

func TestLegacySessionWithoutWorkspaceFallsBackToGlobal(t *testing.T) {
	chat, _, globalWorkspace := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body, `\"ok\":true`) {
			_, _ = w.Write([]byte(sseText("written") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseToolCall("legacy", "write_workspace_file", `"{\"path\":\"legacy.txt\",\"content\":\"legacy\"}"`) + sseDone))
	})

	sess, err := chat.newLegacySession("agent", "agent_ws", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkspaceDir != "" {
		t.Fatalf("legacy constructor unexpectedly assigned a task workspace: %q", sess.WorkspaceDir)
	}
	if err := chat.Send(sess.ID, "write legacy", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(globalWorkspace, "legacy.txt")); err != nil {
		t.Fatalf("legacy session did not use the global workspace: %v", err)
	}
}

func TestNewSessionDefaultsToTaskWorkspace(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.NewSession("agent", "agent_ws", "task")
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkspaceDir == "" {
		t.Fatal("public NewSession must create a task workspace")
	}
	if _, err := os.Stat(sess.WorkspaceDir); err != nil {
		t.Fatalf("task workspace was not created: %v", err)
	}
}

func TestSessionWorkspaceReportsMissingDirectory(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "missing", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sess.WorkspaceDir); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.SessionWorkspace(sess.ID); err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("expected missing-workspace error, got %v", err)
	}
}

func TestExplicitWorkspaceValidationAndConflict(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	chosen := t.TempDir()

	first, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "first", chosen)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspaceDir != filepath.Clean(chosen) {
		t.Fatalf("workspace = %q, want %q", first.WorkspaceDir, filepath.Clean(chosen))
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "duplicate", chosen); err == nil || !strings.Contains(err.Error(), "已被") {
		t.Fatalf("expected duplicate-workspace rejection, got %v", err)
	}
	if err := chat.ArchiveSession(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "reuse", chosen); err != nil {
		t.Fatalf("archived session should release its workspace: %v", err)
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "missing", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing-directory rejection")
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "file", file); err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("expected file-path rejection, got %v", err)
	}
	if _, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "relative", "relative-workdir"); err == nil || !strings.Contains(err.Error(), "绝对") {
		t.Fatalf("expected relative-path rejection, got %v", err)
	}
}

func TestSetSessionWorkspaceBeforeFirstTurnOnly(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "pick", "")
	if err != nil {
		t.Fatal(err)
	}
	chosen := t.TempDir()
	if err := chat.SetSessionWorkspace(sess.ID, chosen); err != nil {
		t.Fatal(err)
	}
	stored := func() config.ChatSession {
		for _, s := range chat.S.Store.Get().Sessions {
			if s.ID == sess.ID {
				return s
			}
		}
		t.Fatal("session missing")
		return config.ChatSession{}
	}()
	if stored.WorkspaceDir != filepath.Clean(chosen) {
		t.Fatalf("stored workspace = %q, want %q", stored.WorkspaceDir, filepath.Clean(chosen))
	}
	if err := chat.Send(sess.ID, "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionWorkspace(sess.ID, t.TempDir()); err == nil || !strings.Contains(err.Error(), "首轮") {
		t.Fatalf("expected post-first-turn lock, got %v", err)
	}
}

func TestConfirmedTaskWriteAppearsInArtifacts(t *testing.T) {
	turn := 0
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_artifact", "write_workspace_file", `"{\"path\":\"approved.md\",\"content\":\"approved\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("approved") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "approval", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "write approved.md", nil); err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatalf("missing confirmation event: %+v", *events)
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	snap, err := NewArtifactService(chat.S).TaskWorkspaceSnapshot(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Added != 1 || len(snap.Artifacts) != 1 || snap.Artifacts[0].Path != "approved.md" {
		t.Fatalf("approved write missing from artifacts: %+v", snap)
	}
}

func TestForkSessionInheritsWorkspaceWithIndependentBaseline(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	source, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "source", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.WorkspaceDir, "before-fork.md"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	branch, err := chat.ForkSession(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.WorkspaceDir != source.WorkspaceDir {
		t.Fatalf("fork workspace = %q, source = %q", branch.WorkspaceDir, source.WorkspaceDir)
	}
	if err := os.WriteFile(filepath.Join(source.WorkspaceDir, "after-fork.md"), []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	art := NewArtifactService(chat.S)
	sourceSnap, err := art.TaskWorkspaceSnapshot(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	branchSnap, err := art.TaskWorkspaceSnapshot(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sourceSnap.Added != 2 {
		t.Fatalf("source baseline should predate both files: %+v", sourceSnap)
	}
	if branchSnap.Added != 1 || len(branchSnap.Changes) != 1 || branchSnap.Changes[0].Path != "after-fork.md" {
		t.Fatalf("fork baseline was not established at fork time: %+v", branchSnap)
	}
}

func TestDeleteSessionRemovesBaselineButKeepsTaskFiles(t *testing.T) {
	chat, store, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "delete", "")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sess.WorkspaceDir, "keep.txt")
	if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	baselinePath := taskSnapshotPath(store, sess.ID)
	if _, err := os.Stat(baselinePath); err != nil {
		t.Fatalf("baseline missing before delete: %v", err)
	}
	if err := chat.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(baselinePath); !os.IsNotExist(err) {
		t.Fatalf("baseline was not removed: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("deleting a session removed its task file: %v", err)
	}
}

func TestArchiveSessionKeepsWorkspaceAndSnapshots(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "archive", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sess.WorkspaceDir, "archived.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(sess.ID, true); err != nil {
		t.Fatal(err)
	}
	snap, err := NewArtifactService(chat.S).TaskWorkspaceSnapshot(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Added != 1 {
		t.Fatalf("archive hid task snapshot: %+v", snap)
	}
	if err := chat.ArchiveSession(sess.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sess.WorkspaceDir, "archived.md")); err != nil {
		t.Fatalf("archive moved or removed task file: %v", err)
	}
}

func TestUnarchiveRejectsWorkspaceNowUsedByActiveTask(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	first, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(first.ID, true); err != nil {
		t.Fatal(err)
	}
	second, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "second", first.WorkspaceDir)
	if err != nil {
		t.Fatalf("archived workspace was not reusable: %v", err)
	}
	if err := chat.ArchiveSession(first.ID, false); err == nil || !strings.Contains(err.Error(), "已被") {
		t.Fatalf("expected unarchive conflict, got %v", err)
	}
	for _, sess := range chat.S.Store.Get().Sessions {
		if sess.ID == first.ID && !sess.Archived {
			t.Fatal("conflicting session was unarchived")
		}
	}
	if err := chat.ArchiveSession(second.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(first.ID, false); err != nil {
		t.Fatalf("unarchive should succeed after the active task released the directory: %v", err)
	}
}

func TestSamePathUsesFilesystemIdentity(t *testing.T) {
	dir := t.TempDir()
	caseVariant := strings.ToUpper(dir)
	if caseVariant == dir {
		caseVariant = strings.ToLower(dir)
	}
	if _, err := os.Stat(caseVariant); err != nil {
		t.Skip("filesystem is case-sensitive; alternate-case path does not exist")
	}
	if !samePath(dir, caseVariant) {
		t.Fatalf("same directory with different case was not recognized: %q vs %q", dir, caseVariant)
	}
}

func TestConcurrentTaskSnapshotsStayIsolated(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	a, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sess config.ChatSession
		body string
	}{{a, "A"}, {b, "B"}} {
		if err := os.WriteFile(filepath.Join(tc.sess.WorkspaceDir, "same.txt"), []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	art := NewArtifactService(chat.S)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		for _, sess := range []config.ChatSession{a, b} {
			wg.Add(1)
			go func(sess config.ChatSession) {
				defer wg.Done()
				snap, err := art.TaskWorkspaceSnapshot(sess.ID)
				if err != nil {
					errs <- err
					return
				}
				if snap.Workspace != sess.WorkspaceDir || snap.Added != 1 || len(snap.Artifacts) != 1 {
					errs <- fmt.Errorf("snapshot for %s crossed workspaces: %+v", sess.ID, snap)
				}
			}(sess)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestParallelTaskWritesAndSnapshotsAreIsolated(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body, `\"ok\":true`) {
			_, _ = w.Write([]byte(sseText("written") + sseDone))
			return
		}
		label := "A"
		if strings.Contains(body, "write-b") {
			label = "B"
		}
		_, _ = w.Write([]byte(sseToolCall("call_"+label, "write_workspace_file",
			fmt.Sprintf(`"{\"path\":\"same.txt\",\"content\":\"%s\"}"`, label)) + sseDone))
	})
	a, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	art := NewArtifactService(chat.S)
	errs := make(chan error, 110)
	var sends sync.WaitGroup
	var snapshots sync.WaitGroup
	sends.Add(2)
	go func() {
		defer sends.Done()
		if err := chat.Send(a.ID, "write-a", nil); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer sends.Done()
		if err := chat.Send(b.ID, "write-b", nil); err != nil {
			errs <- err
		}
	}()
	for i := 0; i < 50; i++ {
		for _, sess := range []config.ChatSession{a, b} {
			snapshots.Add(1)
			go func(sess config.ChatSession) {
				defer snapshots.Done()
				snap, err := art.TaskWorkspaceSnapshot(sess.ID)
				if err != nil {
					errs <- err
					return
				}
				if snap.Workspace != sess.WorkspaceDir {
					errs <- fmt.Errorf("snapshot %s used %q, want %q", sess.ID, snap.Workspace, sess.WorkspaceDir)
				}
			}(sess)
		}
	}
	sends.Wait()
	snapshots.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for _, tc := range []struct {
		sess config.ChatSession
		want string
	}{{a, "A"}, {b, "B"}} {
		file, err := art.OpenForSession(tc.sess.ID, "same.txt")
		if err != nil {
			t.Fatal(err)
		}
		if file.Text != tc.want {
			t.Fatalf("%s read %q, want %q", tc.sess.ID, file.Text, tc.want)
		}
	}
}

func TestSetSessionWorkspaceWaitsForStartingTurn(t *testing.T) {
	requestSeen := make(chan struct{})
	release := make(chan struct{})
	var seen sync.Once
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Do(func() { close(requestSeen) })
		<-release
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("done") + sseDone))
	})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "race", "")
	if err != nil {
		t.Fatal(err)
	}
	sendErr := make(chan error, 1)
	go func() { sendErr <- chat.Send(sess.ID, "hello", nil) }()
	select {
	case <-requestSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
	}
	changeErr := make(chan error, 1)
	chosen := t.TempDir()
	go func() { changeErr <- chat.SetSessionWorkspace(sess.ID, chosen) }()
	select {
	case err := <-changeErr:
		t.Fatalf("workspace rebinding returned before the active turn finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-sendErr; err != nil {
		t.Fatal(err)
	}
	if err := <-changeErr; err == nil || !strings.Contains(err.Error(), "首轮") {
		t.Fatalf("expected post-turn workspace lock, got %v", err)
	}
}
