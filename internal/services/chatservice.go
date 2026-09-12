package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"gorm.io/gorm"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"changeme/internal/config"
)

// Event names emitted to the frontend (registered in main.go).
const (
	EventChatStream = "chat:stream"
)

// appEventHook, when set (tests), receives stream events instead of the
// Wails event bus.
var appEventHook func(ChatStreamEvent)

// AttachmentIn is an image the user attaches to a message (base64 data).
type AttachmentIn struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data string `json:"data"` // base64, no data: prefix
}

// AttachmentOut is an attachment rendered in chat history.
type AttachmentOut struct {
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	DataURL string `json:"dataUrl"`
}

// UsageInfo reports token consumption for a turn or a whole session.
type UsageInfo struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
}

// ChatStreamEvent is streamed to the frontend while a run progresses.
type ChatStreamEvent struct {
	SessionID string         `json:"sessionId"`
	Kind      string         `json:"kind"` // user|delta|message|tool_call|tool_result|done|error
	Author    string         `json:"author"`
	Text      string         `json:"text"`
	ToolName  string         `json:"toolName,omitempty"`
	ToolArgs  map[string]any `json:"toolArgs,omitempty"`
	ToolResp  map[string]any `json:"toolResp,omitempty"`
	// Usage is set on the final assistant message event of a turn.
	Usage *UsageInfo `json:"usage,omitempty"`
	// SessionUsage is the cumulative usage of the session, set on done events.
	SessionUsage *UsageInfo `json:"sessionUsage,omitempty"`
}

// usageOf extracts token usage from an ADK event, if any.
func usageOf(ev *session.Event) *UsageInfo {
	u := ev.LLMResponse.UsageMetadata
	if u == nil {
		return nil
	}
	return &UsageInfo{
		PromptTokens:     int64(u.PromptTokenCount),
		CompletionTokens: int64(u.CandidatesTokenCount),
		TotalTokens:      int64(u.TotalTokenCount),
	}
}

// ChatMessage is a rendered historical message.
type ChatMessage struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"` // user|assistant|tool_call|tool_result
	Author      string          `json:"author"`
	Text        string          `json:"text"`
	ToolName    string          `json:"toolName,omitempty"`
	ToolArgs    map[string]any  `json:"toolArgs,omitempty"`
	ToolResp    map[string]any  `json:"toolResp,omitempty"`
	Attachments []AttachmentOut `json:"attachments,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
}

const (
	maxAttachmentBytes = 6 << 20 // 6 MB decoded per file
	maxAttachments     = 4
)

var attachmentMIMEAllowed = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// buildUserContent assembles the user message content: text plus image parts.
func buildUserContent(text string, atts []AttachmentIn) (*genai.Content, error) {
	content := genai.NewContentFromText(text, genai.RoleUser)
	for i, a := range atts {
		if !attachmentMIMEAllowed[a.MIME] {
			return nil, fmt.Errorf("不支持的附件类型 %s（仅支持图片）", a.MIME)
		}
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			return nil, fmt.Errorf("附件 %s 解码失败", a.Name)
		}
		if len(raw) > maxAttachmentBytes {
			return nil, fmt.Errorf("附件 %s 超过 6MB 限制", a.Name)
		}
		if i >= maxAttachments {
			return nil, fmt.Errorf("最多附带 %d 个附件", maxAttachments)
		}
		content.Parts = append(content.Parts, &genai.Part{
			InlineData: &genai.Blob{MIMEType: a.MIME, Data: raw},
		})
	}
	return content, nil
}

// ChatService manages chat sessions and streaming runs over ADK.
type ChatService struct {
	S *Services

	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	sessions session.Service
	db       *gorm.DB // shared SQLite handle for event trimming (Regenerate)
	appName  string
	userID   string
}

// NewChatService creates the service. db may be nil (disables Regenerate trimming).
func NewChatService(s *Services, sessions session.Service, db *gorm.DB) *ChatService {
	return &ChatService{
		S:        s,
		cancels:  map[string]context.CancelFunc{},
		sessions: sessions,
		db:       db,
		appName:  "workbuddy",
		userID:   "local",
	}
}

// NewSession creates a chat bound to an agent or team.
func (c *ChatService) NewSession(targetType, targetID, title string) (config.ChatSession, error) {
	if targetType != "agent" && targetType != "team" {
		return config.ChatSession{}, fmt.Errorf("invalid target type %q", targetType)
	}
	if title == "" {
		title = "新对话"
	}
	cs := config.ChatSession{
		ID:         newID("sess_"),
		Title:      title,
		TargetType: targetType,
		TargetID:   targetID,
		CreatedAt:  nowUTC(),
		UpdatedAt:  nowUTC(),
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: cs.ID,
	}); err != nil {
		return config.ChatSession{}, err
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{cs}, cfg.Sessions...)
	})
	return cs, err
}

// ListSessions returns all chat sessions, newest first.
func (c *ChatService) ListSessions() []config.ChatSession {
	return c.S.Store.Get().Sessions
}

// RenameSession updates a session title.
func (c *ChatService) RenameSession(id, title string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == id {
				cfg.Sessions[i].Title = title
			}
		}
	})
}

// DeleteSession removes a chat and its stored events.
func (c *ChatService) DeleteSession(id string) error {
	_ = c.sessions.Delete(context.Background(), &session.DeleteRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	})
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Sessions[:0]
		for _, s := range cfg.Sessions {
			if s.ID != id {
				out = append(out, s)
			}
		}
		cfg.Sessions = out
	})
}

// GetSessionMessages renders the stored ADK events for a session.
func (c *ChatService) GetSessionMessages(sessionID string) ([]ChatMessage, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Session == nil {
		return nil, nil
	}
	var out []ChatMessage
	i := 0
	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		i++
		msgs := renderEvent(ev, i)
		out = append(out, msgs...)
	}
	return out, nil
}

func renderEvent(ev *session.Event, seq int) []ChatMessage {
	if ev.Content == nil {
		return nil
	}
	role := "assistant"
	if ev.Author == "user" || ev.Content.Role == genai.RoleUser {
		if ev.Author == "user" {
			role = "user"
		}
	}
	var out []ChatMessage
	base := ChatMessage{
		ID:        fmt.Sprintf("%s-%d", ev.ID, seq),
		Author:    ev.Author,
		Timestamp: ev.Timestamp,
	}
	var textBuf strings.Builder
	flushText := func() {
		if textBuf.Len() > 0 {
			m := base
			m.Kind = role
			m.Text = textBuf.String()
			out = append(out, m)
			textBuf.Reset()
		}
	}
	for _, p := range ev.Content.Parts {
		if p == nil {
			continue
		}
		switch {
		case p.FunctionCall != nil:
			flushText()
			m := base
			m.Kind = "tool_call"
			m.ToolName = p.FunctionCall.Name
			m.ToolArgs = p.FunctionCall.Args
			m.Author = ev.Author
			out = append(out, m)
		case p.FunctionResponse != nil:
			flushText()
			m := base
			m.Kind = "tool_result"
			m.ToolName = p.FunctionResponse.Name
			m.ToolResp = p.FunctionResponse.Response
			m.Author = ev.Author
			out = append(out, m)
		case p.InlineData != nil && p.InlineData.Data != nil:
			flushText()
			if role == "user" {
				m := base
				m.Kind = "user"
				m.Attachments = []AttachmentOut{{
					Name:    "attachment-" + fmt.Sprint(len(out)+1),
					MIME:    p.InlineData.MIMEType,
					DataURL: "data:" + p.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(p.InlineData.Data),
				}}
				out = append(out, m)
			}
		case p.Text != "" && !p.Thought:
			textBuf.WriteString(p.Text)
		}
	}
	flushText()
	return out
}

func addUsage(total, u *UsageInfo) *UsageInfo {
	if u == nil {
		return total
	}
	if total == nil {
		cp := *u
		return &cp
	}
	m := *total
	m.PromptTokens += u.PromptTokens
	m.CompletionTokens += u.CompletionTokens
	m.TotalTokens += u.TotalTokens
	return &m
}

// Send runs one user turn against the session's target agent/team. It blocks
// until the run completes and streams progress via chat:stream events.
func (c *ChatService) Send(sessionID, text string, attachments []AttachmentIn) error {
	return c.sendTurn(sessionID, text, attachments)
}

// trimLastTurn removes all stored events from the last user message onward
// (inclusive) and returns that message's text.
func (c *ChatService) trimLastTurn(sessionID string) (string, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	if resp.Session == nil {
		return "", fmt.Errorf("session %q not found", sessionID)
	}
	text := ""
	var evIDs []string
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		if ev.Author == "user" {
			evIDs = []string{ev.ID}
			var sb strings.Builder
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" && !p.Thought {
					sb.WriteString(p.Text)
				}
			}
			text = sb.String()
			continue
		}
		if text != "" {
			evIDs = append(evIDs, ev.ID)
		}
	}
	if text == "" {
		return "", fmt.Errorf("没有可重新生成的用户消息")
	}
	if c.db != nil && len(evIDs) > 0 {
		if err := c.deleteEvents(sessionID, evIDs); err != nil {
			return "", fmt.Errorf("裁剪历史失败: %w", err)
		}
	}
	return text, nil
}

var _ = context.Background

// Regenerate redoes the last user turn: it trims the trailing conversation
// (from the last user message onward, inclusive) and re-runs the same message.
func (c *ChatService) Regenerate(sessionID string) error {
	text, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, text, nil)
}

// EditAndResend replaces the text of the last user turn and re-runs it.
func (c *ChatService) EditAndResend(sessionID, newText string) error {
	if strings.TrimSpace(newText) == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	if _, err := c.trimLastTurn(sessionID); err != nil {
		return err
	}
	return c.sendTurn(sessionID, newText, nil)
}

// deleteEvents removes stored events by ID (used by Regenerate).
func (c *ChatService) deleteEvents(sessionID string, ids []string) error {
	return c.db.WithContext(context.Background()).Exec(
		"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ? AND id IN ?",
		c.appName, c.userID, sessionID, ids,
	).Error
}

// ExportSession writes the conversation as Markdown into the workspace export
// directory and returns the file path.
func (c *ChatService) ExportSession(sessionID string) (string, error) {
	cfg := c.S.Store.Get()
	var meta *config.ChatSession
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sessionID {
			meta = &cfg.Sessions[i]
			break
		}
	}
	if meta == nil {
		return "", fmt.Errorf("session %q not found", sessionID)
	}
	msgs, err := c.GetSessionMessages(sessionID)
	if err != nil {
		return "", err
	}

	nameOf := func(targetType, id string) string {
		if targetType == "team" {
			for _, t := range cfg.Teams {
				if t.ID == id {
					return t.Name
				}
			}
			return id
		}
		for _, a := range cfg.Agents {
			if a.ID == id {
				return a.Name
			}
		}
		return id
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", meta.Title)
	fmt.Fprintf(&sb, "- 对象: %s %s\n", map[bool]string{true: "团队", false: "Agent"}[meta.TargetType == "team"], nameOf(meta.TargetType, meta.TargetID))
	fmt.Fprintf(&sb, "- 创建时间: %s\n\n---\n\n", meta.CreatedAt.Format("2006-01-02 15:04"))
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			fmt.Fprintf(&sb, "## 🧑 用户\n\n%s\n\n", m.Text)
		case "assistant":
			fmt.Fprintf(&sb, "## 🤖 %s\n\n%s\n\n", m.Author, m.Text)
		case "tool_call":
			args, _ := json.Marshal(m.ToolArgs)
			fmt.Fprintf(&sb, "> 🔧 工具调用 **%s** `%s`\n\n", m.ToolName, string(args))
		case "tool_result":
			resp, _ := json.Marshal(m.ToolResp)
			fmt.Fprintf(&sb, "> ✅ 工具结果 **%s** `%s`\n\n", m.ToolName, string(resp))
		case "message":
			// compatibility with older renders
			fmt.Fprintf(&sb, "## 🤖 %s\n\n%s\n\n", m.Author, m.Text)
		}
	}

	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := sanitizeExportName(meta.Title)
	if base == "" {
		base = "session"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", base, sessionID[len(sessionID)-6:]))
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func sanitizeExportName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r >= 0x4e00 && r <= 0x9fff: // CJK
			sb.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			sb.WriteRune('-')
		}
	}
	return strings.Trim(sb.String(), "-")
}

// sendTurn resolves the session target, builds the agent tree, runs the turn
// and streams progress.
func (c *ChatService) sendTurn(sessionID, text string, attachments []AttachmentIn) error {
	cfg := c.S.Store.Get()
	var target *config.ChatSession
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sessionID {
			target = &cfg.Sessions[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}

	// Build the agent tree from the current configuration.
	var root agent.Agent
	var err error
	if target.TargetType == "team" {
		for _, t := range cfg.Teams {
			if t.ID == target.TargetID {
				root, _, err = c.S.Kit.BuildTeam(t)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("team %q not found", target.TargetID)
		}
	} else {
		for _, a := range cfg.Agents {
			if a.ID == target.TargetID {
				root, err = c.S.Kit.BuildAgent(a)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("agent %q not found", target.TargetID)
		}
	}
	if err != nil {
		return err
	}

	rn, err := runner.New(runner.Config{
		AppName:           c.appName,
		Agent:             root,
		SessionService:    c.sessions,
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}

	emit := func(ev ChatStreamEvent) {
		ev.SessionID = sessionID
		if appEventHook != nil {
			appEventHook(ev)
			return
		}
		if app := application.Get(); app != nil {
			app.Event.Emit(EventChatStream, ev)
		}
	}

	emit(ChatStreamEvent{Kind: "user", Text: text})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mu.Lock()
	if old := c.cancels[sessionID]; old != nil {
		old()
	}
	c.cancels[sessionID] = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.cancels, sessionID)
		c.mu.Unlock()
	}()

	msg, err := buildUserContent(text, attachments)
	if err != nil {
		return err
	}
	var turnUsage *UsageInfo
	var sessionUsage *UsageInfo
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}) {
		if ctx.Err() != nil {
			emit(ChatStreamEvent{Kind: "error", Text: "已取消"})
			return nil
		}
		if err != nil {
			emit(ChatStreamEvent{Kind: "error", Text: err.Error()})
			return nil
		}
		if ev == nil || ev.Content == nil || ev.Author == "user" {
			continue
		}
		if ev.Partial {
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					emit(ChatStreamEvent{Kind: "delta", Author: ev.Author, Text: p.Text})
				}
			}
			continue
		}
		// Final event for this model turn.
		if u := usageOf(ev); u != nil {
			turnUsage = u
			sessionUsage = addUsage(sessionUsage, u)
		}
		var textBuf strings.Builder
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				emit(ChatStreamEvent{Kind: "tool_call", Author: ev.Author,
					ToolName: p.FunctionCall.Name, ToolArgs: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				emit(ChatStreamEvent{Kind: "tool_result", Author: ev.Author,
					ToolName: p.FunctionResponse.Name, ToolResp: p.FunctionResponse.Response})
			case p.Text != "" && !p.Thought:
				textBuf.WriteString(p.Text)
			}
		}
		if textBuf.Len() > 0 {
			emit(ChatStreamEvent{Kind: "message", Author: ev.Author, Text: textBuf.String(), Usage: turnUsage})
			turnUsage = nil
		}
	}
	emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})

	// Auto-title from the first user message.
	c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == sessionID && cfg.Sessions[i].Title == "新对话" {
				t := []rune(strings.TrimSpace(text))
				if len(t) > 30 {
					t = t[:30]
				}
				cfg.Sessions[i].Title = string(t)
			}
			if cfg.Sessions[i].ID == sessionID {
				cfg.Sessions[i].UpdatedAt = nowUTC()
			}
		}
	})
	return nil
}

// Cancel aborts the in-flight run of a session, if any.
func (c *ChatService) Cancel(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.cancels[sessionID]; ok {
		cancel()
	}
}

// IsRunning reports whether a session currently has an in-flight run.
func (c *ChatService) IsRunning(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancels[sessionID] != nil
}
