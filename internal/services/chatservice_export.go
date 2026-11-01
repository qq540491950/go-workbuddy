package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gomarkdown/markdown"
	htmlrenderer "github.com/gomarkdown/markdown/html"

	"go-workbuddy/internal/config"
)

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
		return "", fmt.Errorf("会话 %q 不存在", sessionID)
	}
	msgs, err := c.GetSessionMessages(sessionID)
	if err != nil {
		return "", err
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
	fmt.Fprintf(&sb, "- 对象: %s %s\n", kind, displayNameOf(cfg, meta.TargetType, meta.TargetID))
	fmt.Fprintf(&sb, "- 创建时间: %s\n", meta.CreatedAt.Format("2006-01-02 15:04"))
	if st, err := c.SessionStats(sessionID); err == nil {
		fmt.Fprintf(&sb, "- 统计: %d 条用户消息 · %d 轮回复 · %d 次工具调用 · %d tokens\n",
			st.Messages, st.AssistantTurns, st.ToolCalls, st.TokensTotal)
	}
	if meta.ForkedFrom != "" {
		if src := c.resolveSessionTarget(cfg, meta.ForkedFrom); src != nil {
			fmt.Fprintf(&sb, "- 分叉自: %s\n", src.Title)
		}
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
			if m.ToolName == "plan_todos" {
				if list := renderTodoList(m.ToolResp, false); list != "" {
					fmt.Fprintf(&sb, "> %s\n\n", list)
					continue
				}
			}
			resp, _ := json.Marshal(m.ToolResp)
			fmt.Fprintf(&sb, "> ✅ 工具结果 **%s** `%s`\n\n", m.ToolName, string(resp))
		case "confirm_request":
			fmt.Fprintf(&sb, "> 🛡️ **HITL 确认请求** **%s** `%s`\n\n", m.ToolName, m.CallID)
		case "confirm_result":
			decision := "拒绝"
			if m.ToolResp["confirmed"] == true {
				decision = "批准"
			}
			fmt.Fprintf(&sb, "> 🛡️ **HITL 决策**: %s\n\n", decision)
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
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", base, shortID(sessionID)))
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ExportAllSessions writes every conversation as Markdown into the workspace
// exports directory and returns the number of exported sessions.
func (c *ChatService) ExportAllSessions() (int, error) {
	sessions := c.ListSessions()
	count := 0
	for _, sess := range sessions {
		if _, err := c.ExportSession(sess.ID); err == nil {
			count++
		}
	}
	return count, nil
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
		return "", fmt.Errorf("会话 %q 不存在", sessionID)
	}
	msgs, err := c.GetSessionMessages(sessionID)
	if err != nil {
		return "", err
	}

	// Config-derived strings (titles, agent names) are user-controlled and
	// tool names are model-controlled: everything except the intentionally
	// markdown body text is escaped before it reaches the HTML file.
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", htmlEscapeString(meta.Title))
	kind := "Agent"
	switch meta.TargetType {
	case "team":
		kind = "团队"
	case "remote":
		kind = "远程 Agent"
	}
	fmt.Fprintf(&body, "- 对象: %s %s\n", kind, htmlEscapeString(displayNameOf(cfg, meta.TargetType, meta.TargetID)))
	fmt.Fprintf(&body, "- 创建时间: %s\n\n", meta.CreatedAt.Format("2006-01-02 15:04"))
	if meta.ForkedFrom != "" {
		if src := c.resolveSessionTarget(cfg, meta.ForkedFrom); src != nil {
			fmt.Fprintf(&body, "- 分叉自: %s\n\n", htmlEscapeString(src.Title))
		}
	}
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			fmt.Fprintf(&body, "## 🧑 用户\n\n%s\n\n", m.Text)
		case "assistant":
			fmt.Fprintf(&body, "## 🤖 %s\n\n%s\n\n", m.Author, m.Text)
		case "tool_call":
			args, _ := json.Marshal(m.ToolArgs)
			fmt.Fprintf(&body, "<details><summary>🔧 工具调用 <b>%s</b></summary>\n<pre>%s</pre>\n</details>\n\n",
				htmlEscapeString(m.ToolName), htmlEscapeString(string(args)))
		case "tool_result":
			if m.ToolName == "plan_todos" {
				// Escape first, then swap newlines for <br>: escaping after the
				// substitution would turn the injected <br> into text.
				if list := renderTodoList(m.ToolResp, true); list != "" {
					list = strings.ReplaceAll(list, "\n", "<br>\n")
					fmt.Fprintf(&body, "> %s\n\n", list)
					continue
				}
			}
			resp, _ := json.Marshal(m.ToolResp)
			fmt.Fprintf(&body, "<details><summary>✅ 工具结果 <b>%s</b></summary>\n<pre>%s</pre>\n</details>\n\n",
				htmlEscapeString(m.ToolName), htmlEscapeString(string(resp)))
		case "confirm_request":
			fmt.Fprintf(&body, "> 🛡️ **HITL 确认** %s（`%s`）\n\n", htmlEscapeString(m.ToolName), htmlEscapeString(m.CallID))
		case "confirm_result":
			decision := "拒绝"
			if m.ToolResp["confirmed"] == true {
				decision = "批准"
			}
			fmt.Fprintf(&body, "> 🛡️ **HITL 决策**: %s\n\n", decision)
		case "message":
			// compatibility with older renders
			fmt.Fprintf(&body, "## 🤖 %s\n\n%s\n\n", m.Author, m.Text)
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
</html>`, htmlEscapeString(meta.Title), string(html))

	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.html", sanitizeExportName(meta.Title), shortID(sessionID)))
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

// shortID returns the last 6 characters of id, or the whole id when shorter.
// Direct slicing panics on IDs under 6 chars, and these methods are reachable
// from JS with arbitrary input.
func shortID(id string) string {
	if len(id) <= 6 {
		return id
	}
	return id[len(id)-6:]
}
