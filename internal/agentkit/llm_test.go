package agentkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// sseServer hosts a fake OpenAI-compatible /chat/completions endpoint that
// streams the given chunks as SSE, then [DONE].
func sseServer(t *testing.T, chunks []string, captures *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captures != nil {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*captures = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestOpenAICompatStreamingText(t *testing.T) {
	srv := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{"content":"，世界"}}]}`,
	}, nil)
	defer srv.Close()

	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}
	var collected strings.Builder
	finals := 0
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			for _, p := range resp.Content.Parts {
				collected.WriteString(p.Text)
			}
		} else {
			finals++
		}
	}
	if collected.String() != "你好，世界" {
		t.Fatalf("streamed text = %q", collected.String())
	}
	if finals != 1 {
		t.Fatalf("expected exactly 1 final response, got %d", finals)
	}
}

func TestOpenAICompatToolCallRoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured == nil {
			captured = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if msgs, _ := body["messages"].([]any); len(msgs) > 0 {
			last := msgs[len(msgs)-1].(map[string]any)
			if last["role"] == "tool" {
				_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"北京今天 25 度，晴。"}}]}` + "\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return
			}
		}
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}]}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	temp := float32(0.5)
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("北京天气怎么样", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			Temperature: &temp,
			SystemInstruction: genai.NewContentFromText("你是天气助手", genai.RoleUser),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "get_weather",
				Description: "query weather",
				Parameters: &genai.Schema{
					Type:       genai.TypeObject,
					Properties: map[string]*genai.Schema{"city": {Type: genai.TypeString}},
					Required:   []string{"city"},
				},
			}}}},
		},
	}

	// Turn 1: model should emit a function call.
	var call *genai.FunctionCall
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Partial && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					call = p.FunctionCall
				}
			}
		}
	}
	if call == nil || call.Name != "get_weather" || call.ID != "call_1" {
		t.Fatalf("expected get_weather function call, got %+v", call)
	}
	if call.Args["city"] != "北京" {
		t.Fatalf("expected city=北京, got %v", call.Args)
	}

	// Verify tools reached the request payload.
	if captured["tools"] == nil {
		t.Fatal("tools missing from request payload")
	}
	if captured["temperature"] == nil {
		t.Fatal("temperature missing from request payload")
	}

	// Turn 2: send the function response back; model should answer in text.
	req.Contents = append(req.Contents,
		&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: call}}},
		&genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: call.ID, Name: call.Name, Response: map[string]any{"temp": "25", "sky": "晴"},
		}}}},
	)
	var answer strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Partial {
			answer.WriteString(resp.Content.Parts[0].Text)
		}
	}
	if !strings.Contains(answer.String(), "25") {
		t.Fatalf("expected answer to contain 25, got %q", answer.String())
	}
}

func TestAnthropicStreamingTextAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_time\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"tz\\\":\\\"Asia/Shanghai\\\"}\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	llm, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("现在几点", genai.RoleUser)},
	}
	var text strings.Builder
	var call *genai.FunctionCall
	final := false
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Partial {
			text.WriteString(resp.Content.Parts[0].Text)
		} else {
			final = true
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					call = p.FunctionCall
				}
			}
		}
	}
	if text.String() != "你好" {
		t.Fatalf("text = %q", text.String())
	}
	if !final {
		t.Fatal("missing final response")
	}
	if call == nil || call.Name != "get_time" || call.Args["tz"] != "Asia/Shanghai" {
		t.Fatalf("bad function call: %+v", call)
	}
}

func TestOpenAICompatUsageExtraction(t *testing.T) {
	// Non-streaming: top-level usage.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var got *genai.GenerateContentResponseUsageMetadata
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil {
			got = resp.UsageMetadata
		}
	}
	if got == nil || got.PromptTokenCount != 10 || got.CandidatesTokenCount != 5 || got.TotalTokenCount != 15 {
		t.Fatalf("non-stream usage = %+v", got)
	}

	// Streaming: usage arrives on a choices-less chunk via stream_options.
	var askedForUsage bool
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, askedForUsage = body["stream_options"]
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"hey"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv2.Close()
	llm2, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv2.URL, Model: "m"})
	got = nil
	for resp, err := range llm2.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && !resp.Partial {
			got = resp.UsageMetadata
		}
	}
	if !askedForUsage {
		t.Fatal("stream request missing stream_options.include_usage")
	}
	if got == nil || got.TotalTokenCount != 10 {
		t.Fatalf("stream usage = %+v", got)
	}
}

func TestAnthropicUsageExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()
	llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-test"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var got *genai.GenerateContentResponseUsageMetadata
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && !resp.Partial {
			got = resp.UsageMetadata
		}
	}
	if got == nil || got.PromptTokenCount != 11 || got.CandidatesTokenCount != 4 || got.TotalTokenCount != 15 {
		t.Fatalf("anthropic usage = %+v", got)
	}
}

func TestOpenAICompatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	errGot := ""
	for _, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			errGot = err.Error()
		}
	}
	if !strings.Contains(errGot, "401") {
		t.Fatalf("expected 401 error, got %q", errGot)
	}
}
