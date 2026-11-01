package services

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"go-workbuddy/internal/config"
)

func renderEvent(cfg config.Config, ev *session.Event, seq int) []ChatMessage {
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
		Author:    displayAuthor(cfg, ev.Author),
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

// directAgentConfig builds the implicit target behind a direct chat: the
// default provider's first model with a generic system prompt. Nothing is
// persisted — direct sessions only materialize an agent per turn.
func directAgentConfig(cfg config.Config) (config.AgentConfig, error) {
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].IsDefault {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil && len(cfg.Providers) > 0 {
		provider = &cfg.Providers[0]
	}
	if provider == nil {
		return config.AgentConfig{}, fmt.Errorf("尚未配置模型供应商，请先在「模型配置」页添加并填入 API Key")
	}
	model := ""
	if len(provider.Models) > 0 {
		model = provider.Models[0].ID
	}
	if model == "" {
		return config.AgentConfig{}, fmt.Errorf("默认供应商 %s 尚未配置模型，请先在「模型配置」页添加模型", provider.Name)
	}
	return config.AgentConfig{
		ID:           "direct",
		Name:         directAssistantName,
		ProviderID:   provider.ID,
		Model:        model,
		SystemPrompt: "你是 WorkBuddy 桌面助手。用中文简洁、准确地回答用户问题。",
	}, nil
}

// mentionMatches reports whether body starts with the mention name followed
// by a token boundary (end, space, newline or full-width space).
func mentionMatches(body, name string) bool {
	if !strings.HasPrefix(body, name) {
		return false
	}
	rest := body[len(name):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || strings.HasPrefix(rest, "　")
}

// resolveMention parses a leading "@名称" mention and returns the turn-scoped
// target it names. Candidates cover agents, teams and remote agents; the
// longest matching name wins so "写手" does not shadow "写手助手". Unresolvable
// mentions leave the turn on the session's own target.
func resolveMention(cfg config.Config, text string) (targetType, targetID string, ok bool) {
	if !strings.HasPrefix(text, "@") || len(text) < 2 {
		return "", "", false
	}
	body := text[1:]
	bestLen := 0
	for _, a := range cfg.Agents {
		if a.Name != "" && mentionMatches(body, a.Name) && len(a.Name) > bestLen {
			bestLen, targetType, targetID = len(a.Name), "agent", a.ID
		}
	}
	for _, t := range cfg.Teams {
		if t.Name != "" && mentionMatches(body, t.Name) && len(t.Name) > bestLen {
			bestLen, targetType, targetID = len(t.Name), "team", t.ID
		}
	}
	for _, r := range cfg.RemoteAgents {
		if r.Name != "" && mentionMatches(body, r.Name) && len(r.Name) > bestLen {
			bestLen, targetType, targetID = len(r.Name), "remote", r.ID
		}
	}
	return targetType, targetID, bestLen > 0
}

// turnTargetFor resolves the per-turn target: a leading @mention in the user
// text routes just this turn to the named agent/team/remote, everything else
// stays on the session's own target. The mention stays in the message text —
// history, Regenerate and EditAndResend re-parse it and re-route consistently.
func turnTargetFor(cfg config.Config, target *config.ChatSession, userText string) *config.ChatSession {
	turn := *target
	if userText != "" {
		if typ, id, ok := resolveMention(cfg, userText); ok {
			turn.TargetType, turn.TargetID = typ, id
		}
	}
	return &turn
}

// renderTodoList renders a plan_todos tool result as a compact checklist line
// set for the export renderers (raw JSON payloads are unreadable in exports).
// escape applies HTML escaping to the model-provided content: the HTML export
// renders this straight into the page, and todo contents echo tool arguments
// (model-controlled) — unescaped they would be a stored-XSS vector.
func renderTodoList(resp map[string]any, escape bool) string {
	todos, ok := resp["todos"].([]any)
	if !ok || len(todos) == 0 {
		return ""
	}
	done := 0
	var lines []string
	for _, raw := range todos {
		td, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, _ := td["content"].(string)
		status, _ := td["status"].(string)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if escape {
			content = htmlEscapeString(content)
		}
		mark := "○"
		switch status {
		case "completed":
			mark = "✓"
			done++
		case "in_progress":
			mark = "▶"
		}
		lines = append(lines, "- "+mark+" "+content)
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("**任务计划**（%d/%d 完成）\n\n%s", done, len(lines), strings.Join(lines, "\n"))
}

// displayNameOf resolves a session target's display name from the config
// (team / agent); unknown IDs fall back to the raw id. Shared by both export
// renderers so Markdown and HTML never disagree.
func displayNameOf(cfg config.Config, targetType, id string) string {
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
