package services

import (
	"net/http"
	"os"
	"testing"
)

func TestDeleteArchivedSessions(t *testing.T) {
	chat, store, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	a, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "归档A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "归档B", "")
	if err != nil {
		t.Fatal(err)
	}
	keep, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "活动", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if err := chat.ArchiveSession(id, true); err != nil {
			t.Fatal(err)
		}
	}
	// 归档会话有任务基线文件，删除后必须一并清理。
	baselinePath := taskSnapshotPath(store, a.ID)
	if _, err := os.Stat(baselinePath); err != nil {
		t.Fatalf("baseline must exist before delete: %v", err)
	}

	n, err := chat.DeleteArchivedSessions()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
	left := store.Get().Sessions
	if len(left) != 1 || left[0].ID != keep.ID {
		t.Fatalf("remaining sessions = %+v, want only the active one", left)
	}
	if _, err := os.Stat(baselinePath); !os.IsNotExist(err) {
		t.Fatalf("baseline should be removed, got %v", err)
	}
	// 空归档列表：幂等返回 0。
	n, err = chat.DeleteArchivedSessions()
	if err != nil || n != 0 {
		t.Fatalf("second call = %d, %v; want 0, nil", n, err)
	}
}
