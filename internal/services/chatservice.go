package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"gorm.io/gorm"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/gomarkdown/markdown"
	htmlrenderer "github.com/gomarkdown/markdown/html"

	"changeme/internal/agentkit"
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
	Kind      string         `json:"kind"` // user|delta|message|tool_call|tool_result|confirm_request|done|error
	Author    string         `json:"author"`
	Text      string         `json:"text"`
	ToolName  string         `json:"toolName,omitempty"`
	ToolArgs  map[string]any `json:"toolArgs,omitempty"`
	ToolResp  map[string]any `json:"toolResp,omitempty"`
	// CallID identifies the ADK confirmation interrupt this event refers to
	// (confirm_request events); the answer is sent back via ConfirmToolCall.
	CallID string `json:"callId,omitempty"`
	// Hint is the model-provided explanation shown on the confirmation card.
	Hint string `json:"hint,omitempty"`
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
	Kind        string          `json:"kind"` // user|assistant|tool_call|tool_result|confirm_request|confirm_result
	Author      string          `json:"author"`
	Text        string          `json:"text"`
	ToolName    string          `json:"toolName,omitempty"`
	ToolArgs    map[string]any  `json:"toolArgs,omitempty"`
	ToolResp    map[string]any  `json:"toolResp,omitempty"`
	CallID      string          `json:"callId,omitempty"`
	Hint        string          `json:"hint,omitempty"`
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

	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
	sessions  session.Service
	db        *gorm.DB // shared SQLite handle for event trimming (Regenerate)
	appName   string
	userID    string
	auditPath string // JSONL tool-audit trail; empty = disabled
}

// SetToolAuditPath enables the JSONL tool-call audit trail at path.
func (c *ChatService) SetToolAuditPath(path string) { c.auditPath = path }

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

// NewSession creates a chat bound to an agent, remote A2A agent or team.
func (c *ChatService) NewSession(targetType, targetID, title string) (config.ChatSession, error) {
	if targetType != "agent" && targetType != "team" && targetType != "remote" {
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

// ListSessions returns all chat sessions, pinned first, then newest first.
func (c *ChatService) ListSessions() []config.ChatSession {
	sessions := c.S.Store.Get().Sessions
	sorted := make([]config.ChatSession, len(sessions))
	copy(sorted, sessions)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Pinned != sorted[j].Pinned {
			return sorted[i].Pinned
		}
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})
	return sorted
}

// SearchSessions filters sessions by title OR by message content (a LIKE
// probe over the stored event payloads). Empty query = all sessions
// (pinned first, then most recently active).
func (c *ChatService) SearchSessions(query string) []config.ChatSession {
	sessions := c.ListSessions()
	q := strings.TrimSpace(query)
	if q == "" {
		return sessions
	}
	matched := map[string]bool{}
	for i := range sessions {
		if strings.Contains(sessions[i].Title, q) {
			matched[sessions[i].ID] = true
		}
	}
	if c.db != nil {
		var ids []string
		pattern := "%" + q + "%"
		if err := c.db.Raw("SELECT DISTINCT session_id FROM events WHERE content LIKE ? AND app_name = ? AND user_id = ?",
			pattern, c.appName, c.userID).Pluck("session_id", &ids); err == nil {
			for _, id := range ids {
				matched[id] = true
			}
		}
	}
	out := make([]config.ChatSession, 0, len(matched))
	for _, s := range sessions {
		if matched[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// PinSession toggles the pinned flag of a session.
func (c *ChatService) PinSession(id string, pinned bool) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == id {
				cfg.Sessions[i].Pinned = pinned
			}
		}
	})
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
		case p.FunctionCall != nil && p.FunctionCall.Name == toolconfirmation.FunctionCallName:
			flushText()
			m := base
			m.Kind = "tool_call"
			m.ToolName = p.FunctionCall.Name
			m.ToolArgs = p.FunctionCall.Args
			m.CallID = p.FunctionCall.ID
			if orig, hint := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
				m.Kind = "confirm_request"
				m.ToolName = orig.Name
				m.ToolArgs = orig.Args
				m.Hint = hint
			}
			m.Author = ev.Author
			out = append(out, m)
		case p.FunctionCall != nil:
			flushText()
			m := base
			m.Kind = "tool_call"
			m.ToolName = p.FunctionCall.Name
			m.ToolArgs = p.FunctionCall.Args
			m.Author = ev.Author
			out = append(out, m)
		case p.FunctionResponse != nil && p.FunctionResponse.Name == toolconfirmation.FunctionCallName:
			flushText()
			m := base
			m.Kind = "confirm_result"
			m.CallID = p.FunctionResponse.ID
			m.ToolName = p.FunctionResponse.Name
			m.ToolResp = p.FunctionResponse.Response
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

// confirmRequestInfo extracts the original tool call and human-readable hint
// from an adk_request_confirmation FunctionCall's args. The args survive a
// JSON roundtrip once persisted, so the originalFunctionCall field may be a
// *genai.FunctionCall (live) or a generic map (reloaded history).
func confirmRequestInfo(args map[string]any) (*genai.FunctionCall, string) {
	orig, err := toolconfirmation.OriginalCallFrom(&genai.FunctionCall{Args: args})
	if err != nil {
		return nil, ""
	}
	hint := ""
	if tc, ok := args["toolConfirmation"].(map[string]any); ok {
		hint, _ = tc["hint"].(string)
	}
	return orig, hint
}

// isConfirmationInterrupt reports whether the part is the adk_request_confirmation
// interrupt declared long-running on this event (ADK HITL pause point).
func isConfirmationInterrupt(ev *session.Event, fc *genai.FunctionCall) bool {
	if fc == nil {
		return false
	}
	return fc.Name == toolconfirmation.FunctionCallName && slices.Contains(ev.LongRunningToolIDs, fc.ID)
}

// targetAgentConfig returns the AgentConfig behind a session (nil for teams
// or when the target no longer exists).
func (c *ChatService) targetAgentConfig(cfg config.Config, target *config.ChatSession) *config.AgentConfig {
	if target.TargetType != "agent" {
		return nil
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == target.TargetID {
			return &cfg.Agents[i]
		}
	}
	return nil
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
	kind := "Agent"
	switch meta.TargetType {
	case "team":
		kind = "团队"
	case "remote":
		kind = "远程 Agent"
	}
	fmt.Fprintf(&sb, "- 对象: %s %s\n", kind, nameOf(meta.TargetType, meta.TargetID))
	fmt.Fprintf(&sb, "- 创建时间: %s\n", meta.CreatedAt.Format("2006-01-02 15:04"))
	if st, err := c.SessionStats(sessionID); err == nil {
		fmt.Fprintf(&sb, "- 统计: %d 条用户消息 · %d 轮回复 · %d 次工具调用 · %d tokens\n",
			st.Messages, st.AssistantTurns, st.ToolCalls, st.TokensTotal)
	}
	fmt.Fprintf(&sb, "\n---\n\n")
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

// ExportSessionHTML renders the conversation as a self-contained styled HTML
// file (Markdown pipeline via gomarkdown) and returns the file path.
func (c *ChatService) ExportSessionHTML(sessionID string) (string, error) {
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

	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", meta.Title)
	fmt.Fprintf(&body, "- 对象: %s `%s`\n\n", map[bool]string{true: "团队", false: "Agent"}[meta.TargetType == "team"], meta.TargetID)
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			fmt.Fprintf(&body, "## 🧑 用户\n\n%s\n\n", m.Text)
		case "assistant":
			fmt.Fprintf(&body, "## 🤖 %s\n\n%s\n\n", m.Author, m.Text)
		case "tool_call":
			args, _ := json.Marshal(m.ToolArgs)
			fmt.Fprintf(&body, "<details><summary>🔧 工具调用 <b>%s</b></summary>\n<pre>%s</pre>\n</details>\n\n",
				m.ToolName, htmlEscapeString(string(args)))
		case "tool_result":
			resp, _ := json.Marshal(m.ToolResp)
			fmt.Fprintf(&body, "<details><summary>✅ 工具结果 <b>%s</b></summary>\n<pre>%s</pre>\n</details>\n\n",
				m.ToolName, htmlEscapeString(string(resp)))
		case "confirm_request":
			fmt.Fprintf(&body, "> 🛡️ **HITL 确认** %s（`%s`）\n\n", m.ToolName, m.CallID)
		}
	}

	// Append usage summary + per-day cost table when stats are available.
	if st, err := c.SessionStats(sessionID); err == nil {
		fmt.Fprintf(&body, "\n## 用量统计\n\n")
		fmt.Fprintf(&body, "| 指标 | 数值 |\n| --- | --- |\n")
		fmt.Fprintf(&body, "| 用户消息 | %d |\n", st.Messages)
		fmt.Fprintf(&body, "| 助手轮次 | %d |\n", st.AssistantTurns)
		fmt.Fprintf(&body, "| 工具调用 | %d |\n", st.ToolCalls)
		fmt.Fprintf(&body, "| 总 tokens | %d |\n", st.TokensTotal)
		for _, d := range st.Daily {
			fmt.Fprintf(&body, "| %s（成本估算） | %d tokens / ¥%.4f |\n", d.Date, d.TotalTokens, d.EstimatedCost)
		}
	}

	html := markdown.ToHTML([]byte(body.String()), nil, htmlrenderer.NewRenderer(htmlrenderer.RendererOptions{Flags: htmlrenderer.CommonFlags}))
	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body{max-width:860px;margin:2rem auto;padding:0 1rem;font-family:-apple-system,'Segoe UI',sans-serif;line-height:1.65;color:#1f2328}
h1{border-bottom:2px solid #d0d7de;padding-bottom:.4rem}
h2{margin-top:2rem;font-size:1.15rem}
code{background:#f0f2f4;border-radius:4px;padding:.1em .35em;font-size:.9em}
pre{background:#f6f8fa;padding:.8rem;border-radius:8px;overflow:auto}
pre code{background:none;padding:0}
blockquote{border-left:4px solid #d0d7de;margin:0;padding:.2rem 1rem;color:#57606a;background:#f6f8fa;border-radius:0 8px 8px 0}
table{border-collapse:collapse}td,th{border:1px solid #d0d7de;padding:.35rem .6rem}
img{max-width:100%%}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}h1{border-color:#30363d}code{background:#161b22}pre{background:#161b22}blockquote{border-color:#30363d;background:#161b22;color:#8b949e}td,th{border-color:#30363d}}
@media print{body{max-width:none;margin:0}}
</style>
</head>
<body>
%s
</body>
</html>`, meta.Title, string(html))

	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.html", sanitizeExportName(meta.Title), sessionID[len(sessionID)-6:]))
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// htmlEscapeString escapes text for safe embedding into the HTML export.
func htmlEscapeString(sv string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(sv)
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

// resolveSessionTarget finds the ChatSession metadata for a session ID.
func (c *ChatService) resolveSessionTarget(cfg config.Config, sessionID string) *config.ChatSession {
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sessionID {
			return &cfg.Sessions[i]
		}
	}
	return nil
}

// emitChat delivers a stream event to the test hook or the Wails event bus.
func (c *ChatService) emitChat(sessionID string, ev ChatStreamEvent) {
	ev.SessionID = sessionID
	if appEventHook != nil {
		appEventHook(ev)
		return
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(EventChatStream, ev)
	}
}

// sendTurn sanitizes the user input (guardrails), builds the message content
// and hands the turn to runTurn.
func (c *ChatService) sendTurn(sessionID, text string, attachments []AttachmentIn) error {
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}

	// Layered guardrails: sanitize user input; flag injection attempts.
	agentCfg := c.targetAgentConfig(cfg, target)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	userText := text
	if guardOn {
		gr := agentkit.ApplyInputGuardrail(text)
		userText = gr.Text
		if gr.InjectRisk {
			c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Author: "guardrail",
				Text: "⚠️ 检测到疑似提示注入内容（“" + gr.InjectMatch + "”），已原样发送但请谨慎对待模型输出。"})
		}
		if gr.Redacted {
			c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Author: "guardrail",
				Text: "🔒 已自动遮蔽消息中的疑似密钥/凭据。"})
		}
	}

	msg, err := buildUserContent(userText, attachments)
	if err != nil {
		return err
	}
	return c.runTurn(target, guardOn, msg, text)
}

// ConfirmToolCall answers a pending ADK tool-confirmation interrupt (HITL):
// approved=true resumes the original tool call, false rejects it so the model
// sees the rejection and can adjust.
func (c *ChatService) ConfirmToolCall(sessionID, callID string, approved bool) error {
	if strings.TrimSpace(callID) == "" {
		return fmt.Errorf("缺少确认请求 ID")
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	if resp.Session == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}
	pending := false
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if isConfirmationInterrupt(ev, p.FunctionCall) && p.FunctionCall.ID == callID {
				pending = true
			}
			if p.FunctionResponse != nil && p.FunctionResponse.Name == toolconfirmation.FunctionCallName && p.FunctionResponse.ID == callID {
				return fmt.Errorf("该确认请求已被处理")
			}
		}
	}
	if !pending {
		return fmt.Errorf("未找到待确认的工具调用 %s", callID)
	}
	c.audit(sessionID, "confirm", "user", toolconfirmation.FunctionCallName,
		mustJSON(map[string]any{"callID": callID, "decision": map[bool]string{true: "approved", false: "rejected"}[approved]}), "")

	msg := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID:       callID,
			Name:     toolconfirmation.FunctionCallName,
			Response: map[string]any{"confirmed": approved},
		}}},
	}
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}
	agentCfg := c.targetAgentConfig(cfg, target)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	return c.runTurn(target, guardOn, msg, "")
}

// runTurn builds the agent tree for the session target, runs one turn with
// the given message content and streams progress. It is the shared engine
// behind Send (user text) and ConfirmToolCall (HITL FunctionResponse resume);
// titleFromText is non-empty only for genuine user messages (drives the
// user-bubble event and auto-title).
func (c *ChatService) runTurn(target *config.ChatSession, guardOn bool, msg *genai.Content, titleFromText string) error {
	sessionID := target.ID
	cfg := c.S.Store.Get()
	// Per-agent run guardrails (0 = unlimited); teams fall back to unlimited.
	maxTurnSeconds, maxToolCalls := 0, 0
	var maxSessionTokens int64
	if agentCfg := c.targetAgentConfig(cfg, target); agentCfg != nil {
		maxTurnSeconds, maxToolCalls = agentCfg.MaxTurnSeconds, agentCfg.MaxToolCalls
		maxSessionTokens = agentCfg.MaxSessionTokens
	}
	var budgetBaseline int64
	budgetExceeded := false
	if maxSessionTokens > 0 {
		if st, err := c.SessionStats(sessionID); err == nil {
			budgetBaseline = st.TokensTotal
			budgetExceeded = budgetBaseline > maxSessionTokens
		}
	}

	// Build the agent tree from the current configuration.
	var root agent.Agent
	var err error
	switch target.TargetType {
	case "team":
		for _, t := range cfg.Teams {
			if t.ID == target.TargetID {
				root, _, err = c.S.Kit.BuildTeam(t)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("team %q not found", target.TargetID)
		}
	case "remote":
		for _, r := range cfg.RemoteAgents {
			if r.ID == target.TargetID {
				root, err = agentkit.BuildRemoteAgent(r)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("remote agent %q not found", target.TargetID)
		}
	default:
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

	runnerCfg := runner.Config{
		AppName:           c.appName,
		Agent:             root,
		SessionService:    c.sessions,
		MemoryService:     c.S.Memory,
		AutoCreateSession: true,
	}
	// Context compaction: sliding window over completed turns and/or tail
	// retention at a token threshold. Local LLM-rooted targets only — the
	// default summarizer rides the root agent's model, which remote A2A
	// agents do not have.
	if target.TargetType != "remote" {
		se := cfg.Settings
		if se.CompactionInterval > 0 || se.CompactionTokenThreshold > 0 {
			runnerCfg.Compaction = &compaction.Config{
				CompactionInterval: se.CompactionInterval,
				OverlapSize:        1,
				TokenThreshold:     se.CompactionTokenThreshold,
			}
		}
	}
	rn, err := runner.New(runnerCfg)
	if err != nil {
		return err
	}

	emit := func(ev ChatStreamEvent) { c.emitChat(sessionID, ev) }

	if budgetExceeded && titleFromText != "" {
		// Refuse new user turns only; pending HITL confirm resumes must still
		// run so an in-flight tool round-trip can finish.
		emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("会话 token 预算已满（%d），本轮未执行。", maxSessionTokens)})
		emit(ChatStreamEvent{Kind: "done"})
		return nil
	}
	if titleFromText != "" {
		emit(ChatStreamEvent{Kind: "user", Text: titleFromText})
	}

	ctx, cancel := context.WithCancel(context.Background())
	if maxTurnSeconds > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(maxTurnSeconds)*time.Second)
		defer cancelTimeout()
	}
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

	var turnUsage *UsageInfo
	var sessionUsage *UsageInfo
	toolCallsSeen := 0
	lastReply := ""
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}) {
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("本轮已达最长运行时间（%d 秒），已停止。", maxTurnSeconds)})
			} else {
				emit(ChatStreamEvent{Kind: "error", Text: "已取消"})
			}
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
		if maxSessionTokens > 0 && !budgetExceeded && budgetBaseline+sessionUsageTotal(sessionUsage) > maxSessionTokens {
			budgetExceeded = true
		}
		var textBuf strings.Builder
		toolCallsThisTurn := 0
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil && isConfirmationInterrupt(ev, p.FunctionCall):
				// HITL pause point: surface an actionable confirmation
				// request and let the turn end; the answer arrives via
				// ConfirmToolCall as a FunctionResponse resume.
				name, args := p.FunctionCall.Name, p.FunctionCall.Args
				hint := ""
				if orig, h := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
					name, args, hint = orig.Name, orig.Args, h
				}
				emit(ChatStreamEvent{Kind: "confirm_request", Author: ev.Author,
					ToolName: name, ToolArgs: args, CallID: p.FunctionCall.ID, Hint: hint})
			case p.FunctionCall != nil:
				toolCallsThisTurn++
				c.audit(sessionID, "call", ev.Author, p.FunctionCall.Name, mustJSON(p.FunctionCall.Args), "")
				emit(ChatStreamEvent{Kind: "tool_call", Author: ev.Author,
					ToolName: p.FunctionCall.Name, ToolArgs: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				c.audit(sessionID, "result", ev.Author, p.FunctionResponse.Name,
					mustJSON(p.FunctionResponse.Response), resultErrorOf(p.FunctionResponse.Response))
				emit(ChatStreamEvent{Kind: "tool_result", Author: ev.Author,
					ToolName: p.FunctionResponse.Name, ToolResp: p.FunctionResponse.Response})
			case p.Text != "" && !p.Thought:
				textBuf.WriteString(p.Text)
			}
		}
		if textBuf.Len() > 0 {
			outText := textBuf.String()
			if guardOn {
				outText = agentkit.ApplyOutputGuardrail(outText)
			}
			lastReply = outText
			emit(ChatStreamEvent{Kind: "message", Author: ev.Author, Text: outText, Usage: turnUsage})
			turnUsage = nil
		}
		if maxToolCalls > 0 && toolCallsThisTurn > 0 {
			toolCallsSeen += toolCallsThisTurn
			if toolCallsSeen > maxToolCalls {
				emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("已达单轮工具调用上限（%d 次），本轮已停止。", maxToolCalls)})
				emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})
				cancel()
				return nil
			}
		}
	}
	if budgetExceeded {
		emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("已达会话 token 预算（%d），后续轮次将被拒绝；请开新会话或调整预算。", maxSessionTokens)})
	}
	emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})

	// Auto-remember: opt-in ingestion of the finished turn into long-term
	// memory. Best-effort — failures must not fail the turn.
	if c.S.Store.Get().Settings.AutoRemember && c.S.Memory != nil {
		if resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
			AppName: c.appName, UserID: c.userID, SessionID: sessionID,
		}); err == nil && resp.Session != nil && c.S.Memory != nil {
			_ = c.S.Memory.AddSessionToMemory(context.Background(), resp.Session)
		}
	}

	// Sidebar preview + auto-title from the first user message.
	if lastReply != "" || titleFromText != "" {
		c.S.Store.Update(func(cfg *config.Config) {
			for i := range cfg.Sessions {
				if cfg.Sessions[i].ID != sessionID {
					continue
				}
				cfg.Sessions[i].UpdatedAt = nowUTC()
				if lastReply != "" {
					t := []rune(strings.TrimSpace(lastReply))
					if len(t) > 80 {
						t = t[:80]
					}
					cfg.Sessions[i].Preview = string(t)
				}
				if titleFromText != "" && cfg.Sessions[i].Title == "新对话" {
					t := []rune(strings.TrimSpace(titleFromText))
					if len(t) > 30 {
						t = t[:30]
					}
					cfg.Sessions[i].Title = string(t)
				}
			}
		})
	}
	return nil
}

// ForkSession duplicates a session's full ADK event history into a new chat
// (branching): the user can regenerate/edit/confirm differently in the fork
// without touching the original timeline.
func (c *ChatService) ForkSession(sessionID string) (config.ChatSession, error) {
	if c.db == nil {
		return config.ChatSession{}, fmt.Errorf("会话数据库不可用")
	}
	cfg := c.S.Store.Get()
	src := c.resolveSessionTarget(cfg, sessionID)
	if src == nil {
		return config.ChatSession{}, fmt.Errorf("session %q not found", sessionID)
	}
	id := newID("sess_")
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		return config.ChatSession{}, err
	}

	// Copy every stored event row to the new session. The events table key
	// includes session_id, so ids may repeat across sessions. The column list
	// comes from the schema itself to survive ADK schema updates.
	var cols []string
	if err := c.db.Raw("SELECT name FROM pragma_table_info('events')").Pluck("name", &cols).Error; err != nil {
		return config.ChatSession{}, fmt.Errorf("读取事件表结构失败: %w", err)
	}
	if len(cols) == 0 {
		return config.ChatSession{}, fmt.Errorf("事件表不存在")
	}
	selects := make([]string, len(cols))
	for i, col := range cols {
		if col == "session_id" {
			selects[i] = "? AS session_id"
		} else {
			selects[i] = col
		}
	}
	if err := c.db.Exec(
		fmt.Sprintf("INSERT INTO events (%s) SELECT %s FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
			strings.Join(cols, ","), strings.Join(selects, ",")),
		id, c.appName, c.userID, sessionID,
	).Error; err != nil {
		return config.ChatSession{}, fmt.Errorf("复制会话事件失败: %w", err)
	}

	branch := config.ChatSession{
		ID:         id,
		Title:      strings.TrimSpace(src.Title + "（分支）"),
		TargetType: src.TargetType,
		TargetID:   src.TargetID,
		CreatedAt:  nowUTC(),
		UpdatedAt:  nowUTC(),
		ForkedFrom: sessionID,
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{branch}, cfg.Sessions...)
	})
	return branch, err
}

// RememberSession ingests the whole conversation into long-term memory so
// other sessions (with the memory tool) can recall it later.
func (c *ChatService) RememberSession(sessionID string) (int, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return 0, err
	}
	if resp.Session == nil {
		return 0, fmt.Errorf("session %q not found", sessionID)
	}
	if c.S.Memory == nil {
		return 0, fmt.Errorf("记忆服务不可用")
	}
	if err := c.S.Memory.AddSessionToMemory(context.Background(), resp.Session); err != nil {
		return 0, err
	}
	n, err := c.S.Memory.Count(c.appName, c.userID)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// SessionStats aggregates observability metrics for one conversation.
type SessionStats struct {
	Messages       int         `json:"messages"`
	ToolCalls      int         `json:"toolCalls"`
	ToolFailures   int         `json:"toolFailures"`
	AssistantTurns int         `json:"assistantTurns"`
	TokensIn       int64       `json:"tokensIn"`
	TokensOut      int64       `json:"tokensOut"`
	TokensTotal    int64       `json:"tokensTotal"`
	Daily          []StatDay   `json:"daily,omitempty"`  // per-day token/cost breakdown
	Authors        []AuthorStat `json:"authors,omitempty"` // per-participant breakdown (team timelines)
	FirstAt        *time.Time  `json:"firstAt,omitempty"`
	LastAt         *time.Time  `json:"lastAt,omitempty"`
}

// AuthorStat breaks usage down per conversation participant (team members,
// remote agents); "user" is excluded.
type AuthorStat struct {
	Author    string `json:"author"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"toolCalls"`
	Tokens    int64  `json:"tokens"`
}

// StatDay is one calendar day of usage with an estimated cost using the
// session target's current model prices (¥/1M tokens).
type StatDay struct {
	Date          string  `json:"date"` // YYYY-MM-DD (local)
	PromptTokens  int64   `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens   int64   `json:"totalTokens"`
	EstimatedCost float64 `json:"estimatedCost"`
}

// SessionStats computes aggregate metrics from the stored ADK events.
func (c *ChatService) SessionStats(sessionID string) (*SessionStats, error) {
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	st := &SessionStats{}
	if resp.Session == nil {
		return st, nil
	}
	// Model prices (¥/1M tokens) come from the session target's CURRENT
	// model; historical turns that used another model are approximated.
	priceIn, priceOut := 0.0, 0.0
	cfgNow := c.S.Store.Get()
	if ac := c.targetAgentConfig(cfgNow, target); ac != nil {
		for i := range cfgNow.Providers {
			if cfgNow.Providers[i].ID != ac.ProviderID {
				continue
			}
			for _, mi := range cfgNow.Providers[i].Models {
				if mi.ID == ac.Model {
					priceIn, priceOut = mi.PriceIn, mi.PriceOut
				}
			}
			break
		}
	}
	days := map[string]*StatDay{}
	authors := map[string]*AuthorStat{}

	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		first, last := st.FirstAt, st.LastAt
		if first == nil || ev.Timestamp.Before(*first) {
			ts := ev.Timestamp
			st.FirstAt = &ts
		}
		if last == nil || ev.Timestamp.After(*last) {
			ts := ev.Timestamp
			st.LastAt = &ts
		}
		if ev.Author == "user" {
			st.Messages++
			continue
		}
		if ev.Content == nil {
			continue
		}
		if u := usageOf(ev); u != nil {
			st.TokensIn += u.PromptTokens
			st.TokensOut += u.CompletionTokens
			st.TokensTotal += u.TotalTokens

			as := authors[ev.Author]
			if as == nil {
				as = &AuthorStat{Author: ev.Author}
				authors[ev.Author] = as
			}
			as.Tokens += u.TotalTokens

			day := ev.Timestamp.Local().Format("2006-01-02")
			d := days[day]
			if d == nil {
				d = &StatDay{Date: day}
				days[day] = d
			}
			d.PromptTokens += u.PromptTokens
			d.CompletionTokens += u.CompletionTokens
			d.TotalTokens += u.TotalTokens
			d.EstimatedCost += float64(u.PromptTokens)/1e6*priceIn + float64(u.CompletionTokens)/1e6*priceOut
		}
		if ev.Partial {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				st.ToolCalls++
				if as := authors[ev.Author]; as != nil {
					as.ToolCalls++
				}
			case p.FunctionResponse != nil:
				if strings.Contains(string(mustJSON(p.FunctionResponse.Response)), `"error"`) {
					st.ToolFailures++
				}
			}
		}
		if ev.TurnComplete && !ev.Partial {
			st.AssistantTurns++
			if as := authors[ev.Author]; as != nil {
				as.Turns++
			}
		}
	}
	for name, as := range authors {
		as.Author = name
		st.Authors = append(st.Authors, *as)
	}
	sort.Slice(st.Authors, func(i, j int) bool { return st.Authors[i].Author < st.Authors[j].Author })
	for _, d := range days {
		st.Daily = append(st.Daily, *d)
	}
	sort.Slice(st.Daily, func(i, j int) bool { return st.Daily[i].Date < st.Daily[j].Date })
	return st, nil
}

// auditMaxBytes caps the audit file before rotation (10MB).
const auditMaxBytes = 10 << 20

// audit appends one JSONL line for the tool audit trail. At auditMaxBytes the
// file rotates to <path>.old. Failures are silently ignored: auditing must
// never break the conversation.
func (c *ChatService) audit(sessionID, phase, author, tool string, payload []byte, errMsg string) {
	if c.auditPath == "" || !c.S.Store.Get().Settings.ToolsAudit {
		return
	}
	if st, err := os.Stat(c.auditPath); err == nil && st.Size() >= auditMaxBytes {
		_ = os.Rename(c.auditPath, c.auditPath+".old")
	}
	rec, _ := json.Marshal(map[string]any{
		"ts": nowUTC(), "sessionId": sessionID, "phase": phase,
		"author": author, "tool": tool, "error": errMsg,
		"payload": json.RawMessage(payload),
	})
	if st, err := os.Stat(c.auditPath); err == nil && st.Size()+int64(len(rec))+1 >= auditMaxBytes {
		_ = os.Rename(c.auditPath, c.auditPath+".old")
	}
	f, err := os.OpenFile(c.auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(rec, '\n'))
}

// resultErrorOf extracts the error field from a tool response payload.
func resultErrorOf(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if e, ok := resp["error"].(string); ok {
		return e
	}
	return ""
}

func sessionUsageTotal(u *UsageInfo) int64 {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
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
