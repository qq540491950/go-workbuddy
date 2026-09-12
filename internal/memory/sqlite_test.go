package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// newFixture opens a temp database service plus a real ADK session containing
// two exchanges, exercising the same storage used in production.
func newFixture(t *testing.T) (*SQLiteService, session.Session) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "m.db")))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewSQLiteService(db)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := database.NewSessionServiceFromDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(sessions); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := sessions.Create(ctx, &session.CreateRequest{AppName: "app", UserID: "u", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	get, err := sessions.Get(ctx, &session.GetRequest{AppName: "app", UserID: "u", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{ID: "e1", Author: "user", Timestamp: time.Now(),
			LLMResponse: model.LLMResponse{Content: &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "项目部署在 europe-west4 区"}}}}},
		{ID: "e2", Author: "assistant", Timestamp: time.Now(),
			LLMResponse: model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "记住了。"}}}}},
	}
	for _, ev := range events {
		if err := sessions.AppendEvent(ctx, get.Session, ev); err != nil {
			t.Fatal(err)
		}
	}
	get2, err := sessions.Get(ctx, &session.GetRequest{AppName: "app", UserID: "u", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, get2.Session
}

func TestAddAndSearchMemory(t *testing.T) {
	svc, sess := newFixture(t)
	if err := svc.AddSessionToMemory(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	// Idempotent: re-adding must not duplicate.
	if err := svc.AddSessionToMemory(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	n, err := svc.Count("app", "u")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2 (idempotent)", n)
	}

	resp, err := svc.SearchMemory(context.Background(), &memory.SearchRequest{AppName: "app", UserID: "u", Query: "europe-west4 部署"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) == 0 {
		t.Fatal("expected hits for deployment keyword")
	}
	found := false
	for _, m := range resp.Memories {
		if m.Content != nil {
			for _, p := range m.Content.Parts {
				if p != nil && strings.Contains(p.Text, "europe-west4") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("relevant memory content missing")
	}

	// Unrelated query returns nothing.
	resp, err = svc.SearchMemory(context.Background(), &memory.SearchRequest{AppName: "app", UserID: "u", Query: "quantum"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 0 {
		t.Fatalf("expected no hits, got %d", len(resp.Memories))
	}

	// Clear.
	if err := svc.Clear("app", "u"); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Count("app", "u"); n != 0 {
		t.Fatal("clear failed")
	}
}

func TestSearchIsUserScoped(t *testing.T) {
	svc, sess := newFixture(t)
	if err := svc.AddSessionToMemory(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.SearchMemory(context.Background(), &memory.SearchRequest{AppName: "app", UserID: "other", Query: "europe-west4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 0 {
		t.Fatal("memories leaked across users")
	}
}
