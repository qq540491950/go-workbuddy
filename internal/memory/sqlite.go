// Package memory implements a persistent ADK memory.Service backed by the
// same SQLite database as the session store, giving agents long-term memory
// that survives restarts and spans conversations.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// memoryEntry is the persisted form of a memory.Entry.
type memoryEntry struct {
	ID        string `gorm:"primaryKey"`
	AppName   string `gorm:"index"`
	UserID    string `gorm:"index"`
	Author    string
	Content   string // JSON-encoded genai.Content
	Timestamp time.Time
}

// TableName for gorm.
func (memoryEntry) TableName() string { return "memories" }

// SQLiteService is a persistent memory.Service.
type SQLiteService struct {
	db *gorm.DB
}

// NewSQLiteService migrates the memories table and returns the service.
func NewSQLiteService(db *gorm.DB) (*SQLiteService, error) {
	if err := db.AutoMigrate(&memoryEntry{}); err != nil {
		return nil, err
	}
	return &SQLiteService{db: db}, nil
}

// AddSessionToMemory ingests a session's meaningful events into long-term
// memory. Re-adding the same session is idempotent per event ID.
func (s *SQLiteService) AddSessionToMemory(_ context.Context, sess session.Session) error {
	if sess == nil {
		return fmt.Errorf("memory: nil session")
	}
	for ev := range sess.Events().All() {
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		var textParts []string
		var calls []string
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.Text != "" && !p.Thought:
				textParts = append(textParts, p.Text)
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				calls = append(calls, p.FunctionCall.Name+"("+string(args)+")")
			}
		}
		text := strings.TrimSpace(strings.Join(textParts, "\n"))
		if text == "" && len(calls) == 0 {
			continue
		}
		if len(calls) > 0 {
			text = strings.TrimSpace(text + "\n[工具调用: " + strings.Join(calls, "; ") + "]")
		}
		entry := memoryEntry{
			ID:        ev.ID,
			AppName:   sess.AppName(),
			UserID:    sess.UserID(),
			Author:    ev.Author,
			Content:   string(mustJSON(ev.Content)),
			Timestamp: ev.Timestamp,
		}
		// Idempotent insert: event IDs are unique per session and stable.
		var count int64
		if err := s.db.Model(&memoryEntry{}).Where("id = ?", entry.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := s.db.Create(&entry).Error; err != nil {
			return err
		}
	}
	return nil
}

// SearchMemory finds entries whose text matches the query keywords, ranked by
// hit count (relevance), newest first on ties.
func (s *SQLiteService) SearchMemory(_ context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error) {
	var rows []memoryEntry
	q := s.db.Where("app_name = ? AND user_id = ?", req.AppName, req.UserID)
	keywords := strings.Fields(strings.ToLower(strings.TrimSpace(req.Query)))
	if len(keywords) > 0 {
		conditions := make([]string, 0, len(keywords))
		args := make([]any, 0, len(keywords))
		for _, kw := range keywords {
			conditions = append(conditions, "lower(content) LIKE ?")
			args = append(args, "%"+kw+"%")
		}
		q = q.Where("("+strings.Join(conditions, " OR ")+")", args...)
	}
	if err := q.Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}

	type scored struct {
		row   memoryEntry
		score int
	}
	var scoredRows []scored
	for _, r := range rows {
		lower := strings.ToLower(r.Content)
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if len(keywords) == 0 || score > 0 {
			scoredRows = append(scoredRows, scored{r, score})
		}
	}
	sort.Slice(scoredRows, func(i, j int) bool {
		if scoredRows[i].score != scoredRows[j].score {
			return scoredRows[i].score > scoredRows[j].score
		}
		return scoredRows[i].row.Timestamp.After(scoredRows[j].row.Timestamp)
	})
	if len(scoredRows) > 20 {
		scoredRows = scoredRows[:20]
	}

	resp := &memory.SearchResponse{Memories: []memory.Entry{}}
	for _, sr := range scoredRows {
		var content genai.Content
		if err := json.Unmarshal([]byte(sr.row.Content), &content); err != nil {
			continue
		}
		resp.Memories = append(resp.Memories, memory.Entry{
			ID:        sr.row.ID,
			Content:   &content,
			Author:    sr.row.Author,
			Timestamp: sr.row.Timestamp,
		})
	}
	return resp, nil
}

// Count returns the number of stored memories for a user (diagnostics/UI).
func (s *SQLiteService) Count(appName, userID string) (int64, error) {
	var n int64
	err := s.db.Model(&memoryEntry{}).Where("app_name = ? AND user_id = ?", appName, userID).Count(&n).Error
	return n, err
}

// Clear removes all memories for a user.
func (s *SQLiteService) Clear(appName, userID string) error {
	return s.db.Where("app_name = ? AND user_id = ?", appName, userID).Delete(&memoryEntry{}).Error
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
