package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"google.golang.org/adk/v2/agent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
)

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
// (inclusive) and returns that message's text and image attachments, so
// Regenerate/EditAndResend re-run the turn faithfully. HITL resume events
// (a FunctionResponse stored under the user author by ConfirmToolCall) are
// not user messages and never start a turn boundary — treating them as one
// made every regenerate after a tool confirmation fail.
func (c *ChatService) trimLastTurn(sessionID string) (string, []AttachmentIn, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return "", nil, err
	}
	if resp.Session == nil {
		return "", nil, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	text := ""
	var atts []AttachmentIn
	var evIDs []string
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		if ev.Author == "user" && !hasFunctionResponse(ev) {
			evIDs = []string{ev.ID}
			var sb strings.Builder
			atts = nil
			for _, p := range ev.Content.Parts {
				if p == nil {
					continue
				}
				switch {
				case p.Text != "" && !p.Thought:
					sb.WriteString(p.Text)
				case p.InlineData != nil && len(p.InlineData.Data) > 0:
					atts = append(atts, AttachmentIn{
						Name: "attachment-" + strconv.Itoa(len(atts)+1),
						MIME: p.InlineData.MIMEType,
						Data: base64.StdEncoding.EncodeToString(p.InlineData.Data),
					})
				}
			}
			text = sb.String()
			continue
		}
		if text != "" || len(atts) > 0 {
			evIDs = append(evIDs, ev.ID)
		}
	}
	if text == "" && len(atts) == 0 {
		return "", nil, fmt.Errorf("没有可重新生成的用户消息")
	}
	if c.db != nil && len(evIDs) > 0 {
		if err := c.deleteEvents(sessionID, evIDs); err != nil {
			return "", nil, fmt.Errorf("裁剪历史失败: %w", err)
		}
	}
	return text, atts, nil
}

// hasFunctionResponse reports whether the event carries a FunctionResponse
// part (HITL resumes and tool results), i.e. it is not a plain user message.
func hasFunctionResponse(ev *session.Event) bool {
	for _, p := range ev.Content.Parts {
		if p != nil && p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

var _ = context.Background

// Regenerate redoes the last user turn: it trims the trailing conversation
// (from the last user message onward, inclusive) and re-runs the same message
// with its original attachments.
func (c *ChatService) Regenerate(sessionID string) error {
	// Same contract as DeleteSession: stop the in-flight run BEFORE touching
	// stored events, or the old run keeps appending to the trimmed timeline.
	c.cancelAndWaitBounded(sessionID)
	text, atts, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, text, atts)
}

// EditAndResend replaces the text of the last user turn and re-runs it,
// keeping the turn's original attachments.
func (c *ChatService) EditAndResend(sessionID, newText string) error {
	if strings.TrimSpace(newText) == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	c.cancelAndWaitBounded(sessionID) // see Regenerate: trim only after the old run stopped
	_, atts, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, newText, atts)
}

// deleteEvents removes stored events by ID (used by Regenerate).
func (c *ChatService) deleteEvents(sessionID string, ids []string) error {
	return c.db.WithContext(context.Background()).Exec(
		"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ? AND id IN ?",
		c.appName, c.userID, sessionID, ids,
	).Error
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
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}

	// A leading @mention routes just this turn; guardrails follow the turn's
	// effective target so an @'d agent's own guardrail setting applies.
	turnTarget := turnTargetFor(cfg, target, text)
	// Layered guardrails: sanitize user input; flag injection attempts.
	agentCfg := c.targetAgentConfig(cfg, turnTarget)
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
	return c.runTurn(turnTarget, guardOn, msg, text)
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
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	pending := false
	lastUserText := ""
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		if ev.Content.Role == genai.RoleUser {
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					lastUserText = p.Text
				}
			}
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
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	// A HITL resume must rebuild the SAME agent tree the interrupted turn ran
	// on — including a turn that was routed by a leading @mention in the
	// originating user message.
	turnTarget := turnTargetFor(cfg, target, lastUserText)
	agentCfg := c.targetAgentConfig(cfg, turnTarget)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	return c.runTurn(turnTarget, guardOn, msg, "")
}

// runTurn builds the agent tree for the session target, runs one turn with
// the given message content and streams progress. It is the shared engine
// behind Send (user text) and ConfirmToolCall (HITL FunctionResponse resume);
// titleFromText is non-empty only for genuine user messages (drives the
// user-bubble event and auto-title).
//
// A panic anywhere in the agent/tool stack is converted into an error event:
// one misbehaving MCP server or tool must not take the whole desktop process
// down with the user's unsaved work in it.
func (c *ChatService) runTurn(target *config.ChatSession, guardOn bool, msg *genai.Content, titleFromText string) (err error) {
	sessionID := target.ID
	defer c.recoverRun(sessionID, &err)
	cfg := c.S.Store.Get()
	workspace := target.EffectiveWorkspaceFor(cfg)
	// Per-agent run guardrails (0 = unlimited); teams fall back to unlimited.
	maxTurnSeconds, maxToolCalls := 0, 0
	var maxSessionTokens int64
	if agentCfg := c.targetAgentConfig(cfg, target); agentCfg != nil {
		maxTurnSeconds, maxToolCalls = agentCfg.MaxTurnSeconds, agentCfg.MaxToolCalls
		maxSessionTokens = agentCfg.MaxSessionTokens
	}
	// Safety net: a stalled provider (no bytes, no EOF) would otherwise hang
	// the run forever and the Wails call would never return.
	if maxTurnSeconds <= 0 {
		maxTurnSeconds = defaultTurnTimeoutSeconds
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
	switch target.TargetType {
	case "":
		// Direct chat: no configured target, run the default provider's
		// model with a generic prompt. The per-session model override still
		// applies so the header picker works the same as agent sessions.
		ac, derr := directAgentConfig(cfg)
		if derr != nil {
			return derr
		}
		root, err = c.S.Kit.BuildAgentWithModelAndWorkspace(ac, target.ModelOverride, workspace)
	case "team":
		for _, t := range cfg.Teams {
			if t.ID == target.TargetID {
				root, _, err = c.S.Kit.BuildTeamWithWorkspace(t, workspace)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("团队 %q 不存在", target.TargetID)
		}
	case "remote":
		for _, r := range cfg.RemoteAgents {
			if r.ID == target.TargetID {
				root, err = agentkit.BuildRemoteAgent(r)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("远程 Agent %q 不存在", target.TargetID)
		}
	default:
		for _, a := range cfg.Agents {
			if a.ID == target.TargetID {
				// Per-session model override (chat-header picker): empty keeps
				// the agent's configured model.
				root, err = c.S.Kit.BuildAgentWithModelAndWorkspace(a, target.ModelOverride, workspace)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("Agent %q 不存在", target.TargetID)
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
		emit(ChatStreamEvent{Kind: "user", Text: titleFromText, Attachments: attachmentsOut(msg)})
	}

	ctx, cancel := context.WithCancel(context.Background())
	if maxTurnSeconds > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(maxTurnSeconds)*time.Second)
		defer cancelTimeout()
	}
	defer cancel()
	release := c.registerRun(sessionID, cancel)
	defer release()

	var turnUsage *UsageInfo
	var sessionUsage *UsageInfo
	toolCallsSeen := 0
	lastReply := ""
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}) {
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				msg := fmt.Sprintf("本轮已达最长运行时间（%d 秒），已停止。", maxTurnSeconds)
				emit(ChatStreamEvent{Kind: "error", Text: msg})
				return fmt.Errorf("%s", msg)
			}
			emit(ChatStreamEvent{Kind: "error", Text: "已取消"})
			return context.Canceled
		}
		if err != nil {
			emit(ChatStreamEvent{Kind: "error", Text: err.Error()})
			// Surface the failure to the caller instead of reporting success.
			return fmt.Errorf("run turn: %w", err)
		}
		if ev == nil || ev.Content == nil || ev.Author == "user" {
			continue
		}
		author := displayAuthor(cfg, ev.Author)
		if ev.Partial {
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					text := p.Text
					// Guardrails must cover the streaming path too: deltas reach
					// the UI before the final aggregated message is sanitized.
					// A secret split across two deltas is only caught by the
					// final message event, which replaces the streamed text.
					if guardOn {
						text = agentkit.ApplyOutputGuardrail(text)
					}
					emit(ChatStreamEvent{Kind: "delta", Author: author, Text: text})
				}
			}
			continue
		}
		// Final event for this model turn.
		if u := usageOf(ev); u != nil {
			// A turn with a tool loop / team transfer makes several model
			// calls; accumulate instead of keeping only the last call's
			// numbers (which under-reported multi-step turns).
			turnUsage = addUsage(turnUsage, u)
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
				emit(ChatStreamEvent{Kind: "confirm_request", Author: author,
					ToolName: name, ToolArgs: args, CallID: p.FunctionCall.ID, Hint: hint})
			case p.FunctionCall != nil:
				toolCallsThisTurn++
				c.audit(sessionID, "call", author, p.FunctionCall.Name, mustJSON(p.FunctionCall.Args), "")
				emit(ChatStreamEvent{Kind: "tool_call", Author: author,
					ToolName: p.FunctionCall.Name, ToolArgs: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				c.audit(sessionID, "result", author, p.FunctionResponse.Name,
					mustJSON(p.FunctionResponse.Response), resultErrorOf(p.FunctionResponse.Response))
				emit(ChatStreamEvent{Kind: "tool_result", Author: author,
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
			emit(ChatStreamEvent{Kind: "message", Author: author, Text: outText, Usage: turnUsage})
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
	autoFallback := ""
	if lastReply != "" || titleFromText != "" {
		c.S.Store.Update(func(cfg *config.Config) {
			for i := range cfg.Sessions {
				if cfg.Sessions[i].ID != sessionID {
					continue
				}
				cfg.Sessions[i].UpdatedAt = nowUTC()
				if lastReply != "" {
					cfg.Sessions[i].Preview = truncateRunes(lastReply, 80)
				}
				if titleFromText != "" && cfg.Sessions[i].Title == "新对话" {
					cfg.Sessions[i].Title = truncateRunes(titleFromText, 30)
					autoFallback = cfg.Sessions[i].Title
				}
			}
		})
	}
	// Model-generated title replaces the truncated-text fallback once the
	// first exchange completes (best-effort, async — never blocks the turn).
	// The goroutine carries its own recover: it runs outside runTurn's
	// deferred recoverRun, and a panic here would take the whole process down.
	if autoFallback != "" && cfg.Settings.AutoTitleEnabled() {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("autoTitle panicked (kept fallback title): %v", r)
				}
			}()
			c.autoTitle(sessionID, cfg, autoFallback, titleFromText, lastReply)
		}()
	}
	return nil
}

// autoTitle asks the session's model for a short conversation title and
// replaces the truncated-text fallback. Failures keep the fallback silently.
func (c *ChatService) autoTitle(sessionID string, cfg config.Config, fallback, userText, reply string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return
	}
	ac := c.targetAgentConfig(cfg, target)
	if ac == nil && target.TargetType == "" {
		if d, derr := directAgentConfig(cfg); derr == nil {
			ac = &d
		}
	}
	if ac == nil {
		return
	}
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == ac.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return
	}
	// The side call must respect the per-session model override — the same
	// clamp as BuildAgentWithModel. The custom adapters build the client from
	// the constructor's model, so the override has to happen here.
	modelID := agentkit.EffectiveModel(cfg, ac.ProviderID, ac.Model, target.ModelOverride)
	m, err := agentkit.NewModelFromProvider(*provider, modelID)
	if err != nil {
		return
	}
	// Guardrail the quoted user content just like a normal turn.
	prompt := "根据下面的对话开头，起一个不超过 12 个字的中文标题。只输出标题本身，不要引号、句号或任何解释。\n\n用户: " +
		agentkit.ApplyOutputGuardrail(truncateRunes(userText, 200)) +
		"\n助手: " + agentkit.ApplyOutputGuardrail(truncateRunes(reply, 200))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		Config: &genai.GenerateContentConfig{MaxOutputTokens: 40, Temperature: ptrFloat32(0.3)},
	}
	var title strings.Builder
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil || ctx.Err() != nil {
			return
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text != "" {
					title.WriteString(p.Text)
				}
			}
		}
	}
	clean := strings.TrimSpace(strings.Trim(title.String(), " \"'「」『』。.!？?\n\t"))
	clean = truncateRunes(clean, 30)
	if clean == "" || strings.HasPrefix(clean, "{") {
		return // empty or non-text payload: keep the fallback
	}
	err = c.S.Store.Update(func(cfg2 *config.Config) {
		for i := range cfg2.Sessions {
			// Only replace OUR fallback: a manual rename in between wins.
			if cfg2.Sessions[i].ID == sessionID && cfg2.Sessions[i].Title == fallback {
				cfg2.Sessions[i].Title = clean
			}
		}
	})
	if err != nil {
		return
	}
	// Nudge the sidebar via the dedicated metadata event (a synthetic done
	// would clobber the live usage/context indicators).
	if app := application.Get(); app != nil {
		app.Event.Emit("sessions:updated", sessionID)
	}
}

// truncateRunes caps a string at maxRunes without splitting a UTF-8 rune.
func truncateRunes(s string, maxRunes int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes])
}

func ptrFloat32(f float32) *float32 { return &f }
