package services

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// newGuardrailTestChat wires a ChatService against a scripted model server
// with an agent carrying the given run guardrails.
func newGuardrailTestChat(t *testing.T, maxTurnSeconds, maxToolCalls int, handler http.HandlerFunc) (*ChatService, *[]ChatStreamEvent, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手", ProviderID: "p1", Model: "m",
			BuiltinTools:   []string{"time"},
			MaxTurnSeconds: maxTurnSeconds,
			MaxToolCalls:   maxToolCalls,
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
	chat := NewChatService(svc, sessions, db)

	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })
	return chat, &events, dir
}

// TestGuardrailMaxToolCalls stops the turn after the configured number of
// tool calls instead of letting the model loop forever.
func TestGuardrailMaxToolCalls(t *testing.T) {
	var calls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 0, 2, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) <= 4 {
			// Keep requesting the time tool: rounds 1..4 alternate
			// tool_call → result; the guardrail must stop round 3+.
			_, _ = w.Write([]byte(sseToolCall("c"+fmt.Sprint(calls.Load()), "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})

	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "查询时间", nil); err != nil {
		t.Fatal(err)
	}

	var sawGuardrail, sawDone bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "工具调用上限（2") {
			sawGuardrail = true
		}
		if ev.Kind == "done" {
			sawDone = true
		}
	}
	if !sawGuardrail || !sawDone {
		t.Fatalf("guardrail stop incomplete: guardrail=%v done=%v events=%+v", sawGuardrail, sawDone, *events)
	}
}

// TestGuardrailMaxTurnSeconds aborts a hung model response at the deadline.
func TestGuardrailMaxTurnSeconds(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 1, 0, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("太迟了") + sseDone))
	})

	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "慢慢回答", nil); err != nil {
		t.Fatal(err)
	}
	var sawTimeout bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "最长运行时间") {
			sawTimeout = true
		}
	}
	if !sawTimeout {
		t.Fatalf("timeout guardrail missing: %+v", *events)
	}
}

// TestGuardrailUnlimitedKeepsOldBehavior: zero values mean no guardrail.
func TestGuardrailUnlimitedKeepsOldBehavior(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("直接回答") + sseDone))
	})
	sess, err := chat.NewSession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "error" {
			t.Fatalf("unexpected error with unlimited config: %+v", ev)
		}
	}
	var answered bool
	msgs, _ := chat.GetSessionMessages(sess.ID)
	for _, m := range msgs {
		if m.Kind == "assistant" && strings.Contains(m.Text, "直接回答") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("answer missing: %+v", msgs)
	}
}
