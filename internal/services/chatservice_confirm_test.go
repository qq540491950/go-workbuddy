package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// sseToolCall renders an OpenAI-compatible SSE chunk carrying one tool call.
func sseToolCall(id, name, arguments string) string {
	return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + id + `","function":{"name":"` + name + `","arguments":` + arguments + `}}]}}]}` + "\n\n"
}

func sseText(text string) string {
	return `data: {"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"
}

const sseDone = "data: [DONE]\n\n"

// newConfirmTestChat wires a ChatService against a scripted fake model server
// with an agent that has the files tools and HITL confirmation enabled.
func newConfirmTestChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, string, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_c", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_c", Name: "写手", ProviderID: "prov_c", Model: "fake-mini",
			SystemPrompt: "你负责写文件", BuiltinTools:  []string{"files"},
			ConfirmTools: []string{"files"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store, workspace, dir
}

func hookEvents(t *testing.T) *[]ChatStreamEvent {
	t.Helper()
	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })
	return &events
}

// TestChatToolConfirmationApprove exercises the full HITL loop: the model
// calls the confirmation-gated write tool, the run pauses on the ADK
// adk_request_confirmation interrupt, the user approves via ConfirmToolCall,
// the tool executes and the model produces the final answer.
func TestChatToolConfirmationApprove(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"notes/hitl.txt\",\"content\":\"确认后写入\"}"`) + sseDone))
		case 2:
			body := readAll(r)
			// ADK collapses the confirmation roundtrip for the model: the
			// resumed turn shows the original tool call plus its result
			// (JSON-escaped inside the tool message content).
			if !strings.Contains(body, `\"ok\":true`) {
				t.Errorf("resume request missing approved write result, got: %s", body)
			}
			_, _ = w.Write([]byte(sseText("文件已确认写入。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "把 notes/hitl.txt 写好", nil); err != nil {
		t.Fatal(err)
	}

	// Turn 1 must pause on exactly one actionable confirmation request.
	var req *ChatStreamEvent
	for i := range *events {
		ev := (*events)[i]
		if ev.Kind == "confirm_request" {
			req = &(*events)[i]
		}
	}
	if req == nil {
		t.Fatalf("expected confirm_request event, got: %+v", *events)
	}
	if req.ToolName != "write_workspace_file" || req.CallID == "" {
		t.Fatalf("confirm_request incomplete: %+v", req)
	}
	if req.ToolArgs["path"] != "notes/hitl.txt" {
		t.Fatalf("confirm_request should surface the ORIGINAL call args: %+v", req)
	}
	callID = req.CallID

	// Nothing may be written before approval.
	if _, err := os.Stat(filepath.Join(workspace, "notes", "hitl.txt")); !os.IsNotExist(err) {
		t.Fatal("file must not exist before approval")
	}

	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	// The tool ran after approval and the model answered.
	if data, err := os.ReadFile(filepath.Join(workspace, "notes", "hitl.txt")); err != nil || string(data) != "确认后写入" {
		t.Fatalf("expected approved write, err=%v data=%q", err, data)
	}
	var sawResult, sawAnswer bool
	for _, ev := range *events {
		if ev.Kind == "tool_result" && ev.ToolName == "write_workspace_file" && ev.ToolResp["ok"] == true {
			sawResult = true
		}
		if ev.Kind == "message" && strings.Contains(ev.Text, "已确认写入") {
			sawAnswer = true
		}
	}
	if !sawResult || !sawAnswer {
		t.Fatalf("resume incomplete: result=%v answer=%v events=%+v", sawResult, sawAnswer, *events)
	}

	// History renders the confirm pair with the tool loop.
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReq, sawResultMsg, sawConfirmResult bool
	for _, m := range msgs {
		switch m.Kind {
		case "confirm_request":
			sawReq = m.CallID == callID && m.ToolName == "write_workspace_file" && m.ToolArgs["path"] == "notes/hitl.txt"
		case "tool_result":
			sawResultMsg = sawResultMsg || m.ToolName == "write_workspace_file"
		case "confirm_result":
			sawConfirmResult = m.CallID == callID && m.ToolResp["confirmed"] == true
		}
	}
	if !sawReq || !sawResultMsg || !sawConfirmResult {
		t.Fatalf("history missing confirm flow: req=%v result=%v confirmResult=%v\n%+v", sawReq, sawResultMsg, sawConfirmResult, msgs)
	}

	// Answering again must be rejected, not silently re-run.
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err == nil || !strings.Contains(err.Error(), "已被处理") {
		t.Fatalf("expected double-answer rejection, got %v", err)
	}
}

// TestChatToolConfirmationReject verifies the rejection path: the tool never
// runs and the model sees the rejection.
func TestChatToolConfirmationReject(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"nope.txt\",\"content\":\"不应写入\"}"`) + sseDone))
		case 2:
			body := readAll(r)
			if !strings.Contains(body, "call is rejected") {
				t.Errorf("resume request missing rejection error, got: %s", body)
			}
			_, _ = w.Write([]byte(sseText("好的，已取消写入。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 nope.txt", nil); err != nil {
		t.Fatal(err)
	}
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			callID = (*events)[i].CallID
		}
	}
	if callID == "" {
		t.Fatal("expected confirm_request event")
	}

	if err := chat.ConfirmToolCall(sess.ID, callID, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(workspace, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("rejected write must not touch the workspace")
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawConfirmResult := false
	for _, m := range msgs {
		if m.Kind == "confirm_result" && m.CallID == callID && m.ToolResp["confirmed"] == false {
			sawConfirmResult = true
		}
	}
	if !sawConfirmResult {
		t.Fatalf("history missing rejected confirm_result: %+v", msgs)
	}
}

// TestChatToolConfirmationRestartResume proves a pending confirmation survives
// an app restart: a fresh ChatService over the same session DB re-renders the
// unanswered request from stored events and can still answer it.
func TestChatToolConfirmationRestartResume(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, dir := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"later.txt\",\"content\":\"重启后写入\"}"`) + sseDone))
		case 2:
			if !strings.Contains(readAll(r), `\"ok\":true`) {
				t.Errorf("expected approved write result on post-restart resume")
			}
			_, _ = w.Write([]byte(sseText("重启后确认成功。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	hookEvents(t)

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 later.txt", nil); err != nil {
		t.Fatal(err)
	}

	// "Restart": rebuild the service over the same sessions DB and config.
	db2, sessions2, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc2 := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat2 := NewChatService(svc2, sessions2, db2)

	msgs, err := chat2.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, m := range msgs {
		if m.Kind == "confirm_request" && m.CallID != "" {
			callID = m.CallID
			pending++
		}
		if m.Kind == "confirm_result" {
			t.Fatal("no confirm_result should exist before answering")
		}
	}
	if pending != 1 || callID == "" {
		t.Fatalf("expected exactly one pending confirm_request after restart, got %d", pending)
	}

	if err := chat2.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "later.txt")); err != nil || string(data) != "重启后写入" {
		t.Fatalf("expected post-restart write, err=%v data=%q", err, data)
	}
}

// TestChatToolConfirmationGuards covers argument validation and the default-off
// invariant: agents without ConfirmTools keep the old direct-execution flow.
func TestChatToolConfirmationGuards(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"direct.txt\",\"content\":\"直接写入\"}"`) + sseDone))
		} else {
			_, _ = w.Write([]byte(sseText("完成。") + sseDone))
		}
	})
	events := hookEvents(t)

	if err := chat.ConfirmToolCall("sess_missing", "call_x", true); err == nil {
		t.Fatal("expected error for unknown session")
	}

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmToolCall(sess.ID, "", true); err == nil {
		t.Fatal("expected error for empty callID")
	}
	if err := chat.ConfirmToolCall(sess.ID, "call_nonexistent", true); err == nil {
		t.Fatal("expected error for unknown callID with no pending interrupt")
	}

	// Default-off: clear ConfirmTools and the write runs without any HITL pause.
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			if cfg.Agents[i].ID == "agent_c" {
				cfg.Agents[i].ConfirmTools = nil
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "直接写 direct.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("no confirmation expected when ConfirmTools is empty: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "direct.txt")); err != nil || string(data) != "直接写入" {
		t.Fatalf("expected direct write without HITL, err=%v data=%q", err, data)
	}
}

// TestToolAuditRecordsConfirmation: approve/reject decisions land in the
// JSONL audit trail with their call ID.
func TestToolAuditRecordsConfirmation(t *testing.T) {
	turn := 0
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"audit.txt\",\"content\":\"x\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	chat.SetToolAuditPath(auditPath)
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.ToolsAudit = true
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { appEventHook = nil })

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.ToolsAudit = true
	}); err != nil {
		t.Fatal(err)
	}

	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }

	sess, err := chat.NewSession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 audit.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("no confirm_request captured")
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, `"phase":"confirm"`) || !strings.Contains(doc, "approved") {
		t.Fatalf("confirmation decision missing from audit:\n%s", doc)
	}
	_ = workspace
}
