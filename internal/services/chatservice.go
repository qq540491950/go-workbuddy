package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
	"gorm.io/gorm"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
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
	// Attachments ride on user events so a just-sent message (and a
	// regenerated one) renders its images without waiting for a history
	// reload.
	Attachments []AttachmentOut `json:"attachments,omitempty"`
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
	// defaultTurnTimeoutSeconds bounds a single turn when the agent sets no
	// explicit MaxTurnSeconds. Without it a stalled HTTP stream hangs forever.
	defaultTurnTimeoutSeconds = 10 * 60
)

// directAssistantName is the display name of the implicit direct-chat agent.
const directAssistantName = "助手"

var attachmentMIMEAllowed = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// displayAuthor maps an ADK event author (the sanitized identifier ADK runs
// under) back to the configured display name. Non-ASCII agent names always
// sanitize to the agent_<hash> fallback — without this mapping the chat UI
// would show that hash on every bubble. Unknown authors pass through.
func displayAuthor(cfg config.Config, author string) string {
	if author == "" || author == "user" {
		return author
	}
	for _, a := range cfg.Agents {
		if a.Name != "" && agentkit.SanitizeAgentName(a.Name) == author {
			return a.Name
		}
	}
	for _, t := range cfg.Teams {
		if t.Name != "" && agentkit.SanitizeAgentName(t.Name) == author {
			return t.Name
		}
	}
	if author == agentkit.SanitizeAgentName(directAssistantName) {
		return directAssistantName
	}
	return author
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

// attachmentsOut converts the user message's inline images into the render
// form used by chat history and stream events.
func attachmentsOut(content *genai.Content) []AttachmentOut {
	if content == nil {
		return nil
	}
	var out []AttachmentOut
	for _, p := range content.Parts {
		if p != nil && p.InlineData != nil && len(p.InlineData.Data) > 0 {
			out = append(out, AttachmentOut{
				Name:    "attachment-" + strconv.Itoa(len(out)+1),
				MIME:    p.InlineData.MIMEType,
				DataURL: "data:" + p.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(p.InlineData.Data),
			})
		}
	}
	return out
}

// ChatService manages chat sessions and streaming runs over ADK.
type ChatService struct {
	S *Services

	mu   sync.Mutex
	runs map[string]*runHandle
	// active tracks EVERY in-flight handle per session. runs only holds the
	// current one (used for cancel); an overlapping older run may still be
	// unwinding, and history-mutating entry points must wait for it too.
	active    map[string]map[*runHandle]struct{}
	sessions  session.Service
	db        *gorm.DB // shared SQLite handle for event trimming (Regenerate)
	appName   string
	userID    string
	auditPath string // JSONL tool-audit trail; empty = disabled
	auditMu   sync.Mutex
	// workspaceMu serializes task-workspace assignment/rebinding so two
	// concurrent creations cannot both claim the same physical directory.
	workspaceMu sync.Mutex
	// workspaceLocks serialize one session's turn against workspace rebinding;
	// different tasks can still run and create concurrently.
	workspaceLocks map[string]*sync.Mutex
}

// runHandle identifies one in-flight turn. The pointer identity lets a
// finishing run remove only its own registration; a plain map delete would
// drop a newer run's cancel func when turns overlap.
type runHandle struct {
	cancel context.CancelFunc
	// done is closed by release once the run has fully unwound (no more
	// event writes); history-mutating entry points wait on it.
	done chan struct{}
}

// SetToolAuditPath enables the JSONL tool-call audit trail at path.
func (c *ChatService) SetToolAuditPath(path string) { c.auditPath = path }

// NewChatService creates the service. db may be nil (disables Regenerate trimming).
func NewChatService(s *Services, sessions session.Service, db *gorm.DB) *ChatService {
	return &ChatService{
		S:              s,
		runs:           map[string]*runHandle{},
		active:         map[string]map[*runHandle]struct{}{},
		workspaceLocks: map[string]*sync.Mutex{},
		sessions:       sessions,
		db:             db,
		appName:        "workbuddy",
		userID:         "local",
	}
}

// auditMaxBytes caps the audit file before rotation (10MB).
const auditMaxBytes = 10 << 20

// audit appends one JSONL line for the tool audit trail. At auditMaxBytes the
// file rotates to <path>.old. Failures are silently ignored: auditing must
// never break the conversation. Guarded by a mutex — parallel sessions rotate
// and append concurrently.
func (c *ChatService) audit(sessionID, phase, author, tool string, payload []byte, errMsg string) {
	if c.auditPath == "" || !c.S.Store.Get().Settings.ToolsAudit {
		return
	}
	c.auditMu.Lock()
	defer c.auditMu.Unlock()
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

// recoverRun converts a panic in the agent/tool stack into a stream error plus
// a done event, so the UI always leaves the running state. Used as a deferred
// helper from runTurn.
func (c *ChatService) recoverRun(sessionID string, err *error) {
	r := recover()
	if r == nil {
		return
	}
	c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("运行出错：%v", r)})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "done"})
	*err = fmt.Errorf("run turn panicked: %v", r)
}

// Cancel aborts the in-flight run of a session, if any.
func (c *ChatService) Cancel(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.runs[sessionID]; ok {
		h.cancel()
	}
}

// IsRunning reports whether a session currently has an in-flight run.
func (c *ChatService) IsRunning(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs[sessionID] != nil
}

// registerRun cancels any previous run for the session and records the new
// handle. The returned release must be called (deferred) when the run ends.
func (c *ChatService) registerRun(sessionID string, cancel context.CancelFunc) (release func()) {
	h := &runHandle{cancel: cancel, done: make(chan struct{})}
	c.mu.Lock()
	if c.runs == nil {
		c.runs = map[string]*runHandle{}
	}
	if c.active == nil {
		c.active = map[string]map[*runHandle]struct{}{}
	}
	old := c.runs[sessionID]
	c.runs[sessionID] = h
	set := c.active[sessionID]
	if set == nil {
		set = map[*runHandle]struct{}{}
		c.active[sessionID] = set
	}
	set[h] = struct{}{}
	c.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	return func() {
		c.mu.Lock()
		// Only unregister our own handle: a newer turn may already own the slot.
		if c.runs[sessionID] == h {
			delete(c.runs, sessionID)
		}
		if set := c.active[sessionID]; set != nil {
			delete(set, h)
			if len(set) == 0 {
				delete(c.active, sessionID)
			}
		}
		c.mu.Unlock()
		close(h.done)
	}
}

// cancelAndWaitBounded aborts every in-flight run of a session (the current
// one and any overlapping older handle still unwinding) and waits — bounded
// — until all of them have fully unwound. Regenerate / EditAndResend /
// DeleteSession trim or delete stored events right after this returns; a run
// that is still streaming could otherwise resurrect the deleted history.
func (c *ChatService) cancelAndWaitBounded(sessionID string) {
	c.mu.Lock()
	var handles []*runHandle
	seen := map[*runHandle]bool{}
	for h := range c.active[sessionID] {
		handles = append(handles, h)
		seen[h] = true
	}
	// Defensive: a handle registered before active existed (tests) still
	// gates the trim.
	if h := c.runs[sessionID]; h != nil && !seen[h] {
		handles = append(handles, h)
	}
	c.mu.Unlock()
	if len(handles) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, h := range handles {
		h.cancel()
	}
	for _, h := range handles {
		select {
		case <-h.done:
		case <-ctx.Done():
			// Bounded: a wedged unwind must not hang the RPC forever; the
			// straggler's writes are no worse than the pre-fix behavior.
			return
		}
	}
}
