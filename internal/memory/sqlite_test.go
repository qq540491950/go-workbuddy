package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
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
func newFixture(t *testing.T, texts ...string) (*SQLiteService, session.Session) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "m.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
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
	defaultTexts := []string{"项目部署在 europe-west4 区", "记住了。"}
	if len(texts) == 0 {
		texts = defaultTexts
	}
	var events []*session.Event
	for i, txt := range texts {
		role := genai.RoleUser
		author := "user"
		body := txt
		if i%2 == 1 {
			role, author, body = genai.RoleModel, "assistant", "记住了。"
		}
		events = append(events, &session.Event{
			ID: fmt.Sprintf("e%d", i+1), Author: author, Timestamp: time.Now(),
			LLMResponse: model.LLMResponse{Content: &genai.Content{Role: role, Parts: []*genai.Part{{Text: body}}}},
		})
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

// TestSearchMemoryEscapesLikeWildcards: a query containing % or _ must match
// literally — "%"-wildcard queries used to hit every stored row.
func TestSearchMemoryEscapesLikeWildcards(t *testing.T) {
	svc, sess := newFixture(t, "迁移进度 100% 完成并验证", "新增了 100 个单元测试")
	if err := svc.AddSessionToMemory(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.SearchMemory(context.Background(), &memory.SearchRequest{AppName: "app", UserID: "u", Query: "100%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 1 {
		t.Fatalf("literal %% query must hit only the 100%% entry, got %d hits", len(resp.Memories))
	}
	found := false
	for _, m := range resp.Memories {
		for _, p := range m.Content.Parts {
			if p != nil && strings.Contains(p.Text, "100%") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("literal 100% entry missing from results")
	}
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

// TestSearchMemoryRecencyWindow: the candidate cut is deterministic (newest
// first). Without ORDER BY, SQLite's scan order decides which 200 rows are
// considered, and the newest — typically most relevant — memories could fall
// outside the window once the store grows.
func TestSearchMemoryRecencyWindow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "w.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	svc, err := NewSQLiteService(db)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	const total = 210
	for i := 0; i < total; i++ {
		row := memoryEntry{
			ID:        fmt.Sprintf("e-%03d", i),
			AppName:   "app",
			UserID:    "u",
			Author:    "assistant",
			Content:   string(mustJSON(&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "关键词 部署 说明"}}})),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	resp, err := svc.SearchMemory(context.Background(), &memory.SearchRequest{AppName: "app", UserID: "u", Query: "部署"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 20 {
		t.Fatalf("expected top-20, got %d", len(resp.Memories))
	}
	// All scores tie (one keyword hit each) → newest 20 must win.
	newest := map[string]bool{}
	for i := total - 20; i < total; i++ {
		newest[fmt.Sprintf("e-%03d", i)] = true
	}
	for _, m := range resp.Memories {
		if !newest[m.ID] {
			t.Fatalf("stale candidate %s survived the recency window", m.ID)
		}
	}
}

// TestAddSessionToMemoryConcurrent: parallel ingestion of the same session
// must not error on the UNIQUE id constraint (the old count-then-create used
// to race and abort AutoRemember).
func TestAddSessionToMemoryConcurrent(t *testing.T) {
	svc, sess := newFixture(t)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var err error
			for attempt := 0; attempt < 20; attempt++ {
				err = svc.AddSessionToMemory(context.Background(), sess)
				if err != nil {
					break
				}
				runtime.Gosched()
			}
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent ingest failed: %v", err)
		}
	}
	n, err := svc.Count("app", "u")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2 (deduplicated)", n)
	}
}
