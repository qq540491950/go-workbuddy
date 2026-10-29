package services

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// newRecordingChat is a fixture like newConfirmTestChat whose fake model
// server records every request body, so tests can assert what the model
// actually received across turns.
func newRecordingChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, readAll(r))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_r", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_r", Name: "助手", ProviderID: "prov_r", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store, &bodies
}

// TestRegeneratePreservesAttachments pins the Regenerate contract: the re-run
// must carry the original turn's image attachments, not just its text.
func TestRegeneratePreservesAttachments(t *testing.T) {
	turn := 0
	chat, _, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		_, _ = w.Write([]byte(sseText("第"+string(rune('0'+turn))+"次回答") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	atts := []AttachmentIn{{Name: "shot.png", MIME: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}
	if err := chat.Send(sess.ID, "看看这张图", atts); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], base64.StdEncoding.EncodeToString(png)) {
		t.Fatalf("first send missing image payload")
	}
	// The user stream event must carry the attachment so the optimistic UI
	// renders the image without waiting for a history reload.
	var sawAtt bool
	for _, ev := range *events {
		if ev.Kind == "user" && len(ev.Attachments) == 1 && strings.Contains(ev.Attachments[0].DataURL, base64.StdEncoding.EncodeToString(png)) {
			sawAtt = true
		}
	}
	if !sawAtt {
		t.Fatal("user stream event missing attachment")
	}

	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("regenerated turn lost the image attachment")
	}
	if !strings.Contains((*bodies)[1], "看看这张图") {
		t.Fatal("regenerated turn lost the message text")
	}
	// History must render the attachment again (exactly once).
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	imgs := 0
	for _, m := range msgs {
		if m.Kind == "user" {
			imgs += len(m.Attachments)
		}
	}
	if imgs != 1 {
		t.Fatalf("expected 1 attachment in history, got %d: %+v", imgs, msgs)
	}
}

// TestEditAndResendKeepsAttachments: editing the text must not silently drop
// the images of the original turn.
func TestEditAndResendKeepsAttachments(t *testing.T) {
	chat, _, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("回答") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G'}
	atts := []AttachmentIn{{Name: "a.png", MIME: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}
	if err := chat.Send(sess.ID, "原始问题", atts); err != nil {
		t.Fatal(err)
	}
	if err := chat.EditAndResend(sess.ID, "改后的问题"); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("edited resend lost the image attachment")
	}
	if !strings.Contains((*bodies)[1], "改后的问题") {
		t.Fatal("edited resend missing the new text")
	}
}

// TestRegenerateAfterHitlConfirm is the regression test for the trim boundary:
// a HITL resume is stored under the user author (a FunctionResponse event) and
// used to swallow the real user message, making every subsequent Regenerate
// fail with "没有可重新生成的用户消息".
func TestRegenerateAfterHitlConfirm(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"re.txt\",\"content\":\"v\"}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("已确认写入。") + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("重新生成的回复") + sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 re.txt", nil); err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("expected confirm_request")
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "re.txt")); err != nil {
		t.Fatal(err)
	}

	// Regenerate must work: the whole confirm trail is trimmed and the
	// original user message re-sent.
	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatalf("regenerate after HITL confirm: %v", err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Text != "写 re.txt" || msgs[1].Text != "重新生成的回复" {
		t.Fatalf("post-HITL regenerate history = %+v", msgs)
	}
	for _, m := range msgs {
		if m.Kind == "confirm_request" || m.Kind == "confirm_result" || m.Kind == "tool_result" {
			t.Fatalf("confirm trail not trimmed: %+v", m)
		}
	}
}

// TestExportHTMLIncludesHITLAndNames pins the HTML export parity: HITL
// decisions, resolved target names and metadata lines all appear, like in the
// Markdown export.
func TestExportHTMLIncludesHITLAndNames(t *testing.T) {
	turn := 0
	var callID string
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"h.html\",\"content\":\"v\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 h.html", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	path, err := chat.ExportSessionHTML(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{"HITL 确认", "HITL 决策", "批准", "创建时间", "写手"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("HTML export missing %q", want)
		}
	}
}

// TestDeleteSessionCancelsRunningTurn: deleting a session must abort its
// in-flight run instead of leaving it burning tokens until the timeout.
func TestDeleteSessionCancelsRunningTurn(t *testing.T) {
	chat, _, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Hold the stream open until the client goes away (cancel), not a
		// fixed sleep — t.Cleanup waits for handlers to finish.
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		_, _ = w.Write([]byte(sseText("太迟了") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- chat.Send(sess.ID, "慢慢回答", nil) }()

	deadline := time.Now().Add(5 * time.Second)
	for !chat.IsRunning(sess.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !chat.IsRunning(sess.ID) {
		t.Fatal("turn never started")
	}
	start := time.Now()
	if err := chat.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled send to report an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send did not return after session delete")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("delete blocked %v", elapsed)
	}
	if chat.IsRunning(sess.ID) {
		t.Fatal("run still registered after delete")
	}
}

// TestTurnUsageAccumulatesAcrossToolLoop: a turn that makes several model
// calls reports the SUM of their usage on the final message event, not just
// the last call's numbers.
func TestTurnUsageAccumulatesAcrossToolLoop(t *testing.T) {
	turn := 0
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			// Tool call round: 10 prompt + 4 completion (fixture usage is
			// appended by sseDone-style usage chunk below).
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"u.txt\",\"content\":\"v\"}"`) +
				`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}` + "\n\n" + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") +
			`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140}}` + "\n\n" + sseDone))
	})
	events := hookEvents(t)

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			cfg.Agents[i].ConfirmTools = nil // no HITL: direct execution
		}
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 u.txt", nil); err != nil {
		t.Fatal(err)
	}
	var msgUsage *UsageInfo
	for i := range *events {
		ev := (*events)[i]
		if ev.Kind == "message" && ev.Usage != nil {
			msgUsage = ev.Usage
		}
	}
	if msgUsage == nil {
		t.Fatal("final message carried no usage")
	}
	if msgUsage.PromptTokens != 110 || msgUsage.CompletionTokens != 44 || msgUsage.TotalTokens != 154 {
		t.Fatalf("usage not accumulated: %+v", msgUsage)
	}
}

// TestChatPreviewTruncatedByRune: the sidebar preview never splits a UTF-8
// rune (multi-byte Chinese text at the 80-rune boundary).
func TestChatPreviewTruncatedByRune(t *testing.T) {
	chat, _, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText(strings.Repeat("汉", 120)) + sseDone))
	})
	hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "长回复", nil); err != nil {
		t.Fatal(err)
	}
	var preview string
	for _, s := range chat.S.Store.Get().Sessions {
		if s.ID == sess.ID {
			preview = s.Preview
		}
	}
	if got := len([]rune(preview)); got != 80 {
		t.Fatalf("preview runes = %d, want 80", got)
	}
}

// TestRenameSessionAuditOnlyOnSuccess keeps the lifecycle audit honest:
// renaming an unknown session fails and must not leave a "renamed" record.
func TestRenameSessionAuditOnlyOnSuccess(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var reply atomic.Value
	reply.Store("ok")
	chat, sess, store := newChatFixture(t, &reply)
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}

	if err := chat.RenameSession(sess.ID, "新名字"); err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession("sess_missing", "不该成功"); err == nil {
		t.Fatal("expected error renaming unknown session")
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"phase":"session_renamed"`) != 1 {
		t.Fatalf("expected exactly one renamed audit record:\n%s", data)
	}
}

// TestSearchSessionsEscapesLikeWildcards: searching "符%" must match the
// literal text only — unescaped %/_ turned content search into a fuzzy
// substring match.
func TestSearchSessionsEscapesLikeWildcards(t *testing.T) {
	var reply atomic.Value
	reply.Store("ok")
	chat, sess1, _ := newChatFixture(t, &reply)

	sess2, err := chat.NewSession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess1.ID, "包含符%号的内容", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess2.ID, "包含符号的内容", nil); err != nil {
		t.Fatal(err)
	}

	matched := chat.SearchSessions("符%")
	if len(matched) != 1 || matched[0].ID != sess1.ID {
		for _, m := range matched {
			t.Logf("matched: %s %s", m.ID, m.Title)
		}
		t.Fatalf("wildcard not escaped: got %d matches", len(matched))
	}
}
