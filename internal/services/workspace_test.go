package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

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

	sess, err := chat.NewSession("agent", "agent_ws", "legacy")
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
