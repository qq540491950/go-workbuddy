// Package agentkit builds ADK agents from user configuration.
// This file implements model.LLM adapters for OpenAI-compatible
// Chat Completions APIs and the Anthropic Messages API, both with
// streaming and function calling, so any provider can be configured.
package agentkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// httpClient is shared by both adapters. Streaming responses are bounded by
// the request context, never by a client-wide timeout.
var httpClient = &http.Client{}

func readErrBody(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, 4096))
	return strings.TrimSpace(string(b))
}

// ---------------------------------------------------------------------------
// genai helpers shared by both adapters

type adkFunctionDecl struct {
	name        string
	description string
	parameters  *genai.Schema
}

func configTools(req *model.LLMRequest) []adkFunctionDecl {
	var out []adkFunctionDecl
	if req.Config == nil {
		return out
	}
	for _, t := range req.Config.Tools {
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			out = append(out, adkFunctionDecl{name: fd.Name, description: fd.Description, parameters: fd.Parameters})
		}
	}
	return out
}

func schemaToJSONSchema(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object"}
	}
	m := map[string]any{}
	if s.Type != "" {
		m["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Items != nil {
		m["items"] = schemaToJSONSchema(s.Items)
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for k, v := range s.Properties {
			props[k] = schemaToJSONSchema(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	return m
}

func systemText(req *model.LLMRequest) string {
	if req.Config == nil || req.Config.SystemInstruction == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		if p != nil && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func temperatureOf(req *model.LLMRequest) *float32 {
	if req.Config == nil {
		return nil
	}
	return req.Config.Temperature
}

func maxTokensOf(req *model.LLMRequest) int64 {
	if req.Config == nil {
		return 0
	}
	return int64(req.Config.MaxOutputTokens)
}

// withUsage attaches provider usage to a final response so the ADK runner
// stores it and the UI can surface token counts.
func withUsage(resp *model.LLMResponse, u *oaUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.PromptTokens),
		CandidatesTokenCount: int32(u.CompletionTokens),
		TotalTokenCount:      int32(u.TotalTokens),
	}
	return resp
}

func withUsageA(resp *model.LLMResponse, u *anUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	total := u.InputTokens + u.OutputTokens
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.InputTokens),
		CandidatesTokenCount: int32(u.OutputTokens),
		TotalTokenCount:      int32(total),
	}
	return resp
}

func argsToMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

// finalLLMResponse assembles the single non-partial event that the ADK
// Runner processes: complete text plus every function call.
func finalLLMResponse(modelName, text string, parts []*genai.Part) *model.LLMResponse {
	if text != "" || len(parts) == 0 {
		parts = append([]*genai.Part{{Text: text}}, parts...)
	}
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: parts},
		TurnComplete: true,
	}
}

func textDelta(modelName, delta string) *model.LLMResponse {
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: delta}}},
		Partial:      true,
	}
}

func scanSSE(body io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	return sc
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions compatible adapter

// OpenAICompatConfig configures the OpenAI-compatible adapter.
type OpenAICompatConfig struct {
	BaseURL string // e.g. https://api.deepseek.com/v1
	APIKey  string
	Model   string
}

type openaiCompatLLM struct {
	cfg OpenAICompatConfig
}

// NewOpenAICompat builds a model.LLM speaking the OpenAI Chat Completions
// protocol (OpenAI, DeepSeek, Kimi, Qwen, Ollama, vLLM, ...).
func NewOpenAICompat(cfg OpenAICompatConfig) (model.LLM, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai-compatible: model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &openaiCompatLLM{cfg: cfg}, nil
}

func (o *openaiCompatLLM) Name() string { return o.cfg.Model }

type oaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaRequest struct {
	Model         string      `json:"model"`
	Messages      []oaMessage `json:"messages"`
	Stream        bool        `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Tools       []oaTool `json:"tools,omitempty"`
	MaxTokens   int64    `json:"max_tokens,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
}

type oaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type oaTool struct {
	Type     string    `json:"type"`
	Function oaToolDef `json:"function"`
}
type oaToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (o *openaiCompatLLM) buildMessages(req *model.LLMRequest) []oaMessage {
	var msgs []oaMessage
	if sys := systemText(req); sys != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: sys})
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		var textParts []string
		var calls []oaToolCall
		var responses []*genai.FunctionResponse
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				id := p.FunctionCall.ID
				if id == "" {
					id = p.FunctionCall.Name
				}
				tc := oaToolCall{ID: id, Type: "function"}
				tc.Function.Name = p.FunctionCall.Name
				tc.Function.Arguments = string(args)
				calls = append(calls, tc)
			case p.FunctionResponse != nil:
				responses = append(responses, p.FunctionResponse)
			case p.Text != "":
				textParts = append(textParts, p.Text)
			}
		}
		switch {
		case len(responses) > 0:
			for _, fr := range responses {
				payload, _ := json.Marshal(fr.Response)
				if len(fr.Response) == 0 {
					payload = []byte(`{"ok":true}`)
				}
				id := fr.ID
				if id == "" {
					id = fr.Name
				}
				msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: id, Name: fr.Name, Content: string(payload)})
			}
		case len(calls) > 0:
			m := oaMessage{Role: "assistant", ToolCalls: calls}
			if joined := strings.Join(textParts, ""); joined != "" {
				m.Content = joined
			}
			msgs = append(msgs, m)
		default:
			joined := strings.Join(textParts, "")
			if joined == "" {
				continue
			}
			role := "user"
			if c.Role == genai.RoleModel {
				role = "assistant"
			}
			msgs = append(msgs, oaMessage{Role: role, Content: joined})
		}
	}
	return msgs
}

func (o *openaiCompatLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		body := oaRequest{Model: o.cfg.Model, Messages: o.buildMessages(req), Stream: stream,
			MaxTokens: maxTokensOf(req), Temperature: temperatureOf(req)}
		if stream {
			body.StreamOptions = &struct {
				IncludeUsage bool `json:"include_usage"`
			}{IncludeUsage: true}
		}
		var usage *oaUsage
		for _, fd := range configTools(req) {
			body.Tools = append(body.Tools, oaTool{Type: "function", Function: oaToolDef{
				Name: fd.name, Description: fd.description, Parameters: schemaToJSONSchema(fd.parameters),
			}})
		}
		resp, err := o.post(ctx, body)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()

		var sb strings.Builder
		calls := map[int]*oaAccumCall{}
		var order []int
		addCall := func(idx int, id, name, argDelta string) *oaAccumCall {
			a := calls[idx]
			if a == nil {
				a = &oaAccumCall{id: id, name: name}
				calls[idx] = a
				order = append(order, idx)
			}
			if id != "" {
				a.id = id
			}
			if name != "" {
				a.name = name
			}
			a.args.WriteString(argDelta)
			return a
		}

		if !stream {
			var full struct {
				Choices []struct {
					Message struct {
						Content   string       `json:"content"`
						ToolCalls []oaToolCall `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
				Usage *oaUsage `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
				yield(nil, err)
				return
			}
			if full.Error != nil && full.Error.Message != "" {
				yield(nil, fmt.Errorf("openai-compatible: %s", full.Error.Message))
				return
			}
			usage = full.Usage
			if len(full.Choices) > 0 {
				sb.WriteString(full.Choices[0].Message.Content)
				for _, tc := range full.Choices[0].Message.ToolCalls {
					addCall(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
				}
			}
			yield(withUsage(o.final(sb.String(), calls, order), usage), nil)
			return
		}

		scanner := scanSSE(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var chunk oaChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if chunk.Error != nil && chunk.Error.Message != "" {
				yield(nil, fmt.Errorf("openai-compatible: %s", chunk.Error.Message))
				return
			}
			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = chunk.Usage
				}
				continue
			}
			d := chunk.Choices[0].Delta
			if d.Content != "" {
				sb.WriteString(d.Content)
				if !yield(textDelta(o.cfg.Model, d.Content), nil) {
					return
				}
			}
			for _, tc := range d.ToolCalls {
				addCall(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				yield(nil, ctx.Err())
				return
			}
			yield(nil, err)
			return
		}
		yield(withUsage(o.final(sb.String(), calls, order), usage), nil)
	}
}

func (o *openaiCompatLLM) final(text string, calls map[int]*oaAccumCall, order []int) *model.LLMResponse {
	var parts []*genai.Part
	for _, idx := range order {
		c := calls[idx]
		args := argsToMap([]byte(c.args.String()))
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: c.id, Name: c.name, Args: args}})
	}
	return finalLLMResponse(o.cfg.Model, text, parts)
}

type oaAccumCall struct {
	id, name string
	args     strings.Builder
}

func (o *openaiCompatLLM) post(ctx context.Context, body oaRequest) (*http.Response, error) {
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("openai-compatible %s: HTTP %d: %s", o.cfg.Model, resp.StatusCode, readErrBody(resp.Body))
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Anthropic Messages adapter

// AnthropicConfig configures the Anthropic adapter.
type AnthropicConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type anthropicLLM struct {
	cfg AnthropicConfig
}

// NewAnthropic builds a model.LLM speaking the Anthropic Messages API.
func NewAnthropic(cfg AnthropicConfig) (model.LLM, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &anthropicLLM{cfg: cfg}, nil
}

func (a *anthropicLLM) Name() string { return a.cfg.Model }

type anBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anMessage struct {
	Role    string    `json:"role"`
	Content []anBlock `json:"content"`
}

type anRequest struct {
	Model     string   `json:"model"`
	Messages  []anMessage `json:"messages"`
	System    string      `json:"system,omitempty"`
	MaxTokens int64       `json:"max_tokens"`
	Tools     []anTool    `json:"tools,omitempty"`
	Stream    bool        `json:"stream"`
	Temp      *float32    `json:"temperature,omitempty"`
}

type anTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (a *anthropicLLM) buildMessages(req *model.LLMRequest) []anMessage {
	var msgs []anMessage
	appendBlock := func(role genai.Role, b anBlock) {
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == string(role) {
			msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, b)
		} else {
			msgs = append(msgs, anMessage{Role: string(role), Content: []anBlock{b}})
		}
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		var role genai.Role = genai.RoleUser
		if c.Role == genai.RoleModel {
			role = genai.RoleModel
		}
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				id := p.FunctionCall.ID
				if id == "" {
					id = "call_" + p.FunctionCall.Name
				}
				appendBlock(genai.RoleModel, anBlock{Type: "tool_use", ID: id, Name: p.FunctionCall.Name, Input: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				id := p.FunctionResponse.ID
				if id == "" {
					id = "call_" + p.FunctionResponse.Name
				}
				payload, _ := json.Marshal(p.FunctionResponse.Response)
				if len(p.FunctionResponse.Response) == 0 {
					payload = []byte(`{"ok":true}`)
				}
				appendBlock(genai.RoleUser, anBlock{Type: "tool_result", ToolUseID: id, Content: string(payload)})
			case p.Text != "":
				appendBlock(role, anBlock{Type: "text", Text: p.Text})
			}
		}
	}
	if len(msgs) > 0 && msgs[0].Role == string(genai.RoleModel) {
		msgs = append([]anMessage{{Role: string(genai.RoleUser), Content: []anBlock{{Type: "text", Text: "(start)"}}}}, msgs...)
	}
	return msgs
}

func (a *anthropicLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		maxTok := maxTokensOf(req)
		if maxTok <= 0 {
			maxTok = 8192
		}
		body := anRequest{Model: a.cfg.Model, Messages: a.buildMessages(req), System: systemText(req),
			MaxTokens: maxTok, Stream: stream, Temp: temperatureOf(req)}
		var usage *anUsage
		for _, fd := range configTools(req) {
			body.Tools = append(body.Tools, anTool{
				Name: fd.name, Description: fd.description, InputSchema: schemaToJSONSchema(fd.parameters),
			})
		}
		resp, err := a.post(ctx, body)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()

		var sb strings.Builder
		var tools []*anAccumTool
		getTool := func(idx int) *anAccumTool {
			for len(tools) <= idx {
				tools = append(tools, nil)
			}
			return tools[idx]
		}

		if !stream {
			var full struct {
				Content []anBlock `json:"content"`
				Usage   *anUsage  `json:"usage"`
				Error   *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
				yield(nil, err)
				return
			}
			if full.Error != nil && full.Error.Message != "" {
				yield(nil, fmt.Errorf("anthropic: %s", full.Error.Message))
				return
			}
			usage = full.Usage
			for _, b := range full.Content {
				switch b.Type {
				case "text":
					sb.WriteString(b.Text)
				case "tool_use":
					t := &anAccumTool{id: b.ID, name: b.Name}
					t.input.Write(argsToJSON(b.Input))
					tools = append(tools, t)
				}
			}
			yield(a.final(sb.String(), tools), nil)
			return
		}

		scanner := scanSSE(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
				ContentBlock struct {
					Type  string          `json:"type"`
					ID    string          `json:"id"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content_block"`
				Message *struct {
					Usage *anUsage `json:"usage"`
				} `json:"message"`
				Usage *anUsage `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "error":
				msg := "unknown error"
				if ev.Error != nil {
					msg = ev.Error.Message
				}
				yield(nil, fmt.Errorf("anthropic: %s", msg))
				return
			case "message_start":
				if ev.Message != nil && ev.Message.Usage != nil {
					usage = &anUsage{InputTokens: ev.Message.Usage.InputTokens}
				}
			case "message_delta":
				if ev.Usage != nil && usage != nil {
					usage.OutputTokens = ev.Usage.OutputTokens
				}
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					getTool(ev.Index) // reserve slot
					tools[ev.Index] = &anAccumTool{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						sb.WriteString(ev.Delta.Text)
						if !yield(textDelta(a.cfg.Model, ev.Delta.Text), nil) {
							return
						}
					}
				case "input_json_delta":
					if t := getTool(ev.Index); t != nil {
						t.input.WriteString(ev.Delta.PartialJSON)
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				yield(nil, ctx.Err())
				return
			}
			yield(nil, err)
			return
		}
		yield(withUsageA(a.final(sb.String(), tools), usage), nil)
	}
}

func (a *anthropicLLM) final(text string, tools []*anAccumTool) *model.LLMResponse {
	var parts []*genai.Part
	for _, t := range tools {
		if t == nil {
			continue
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
			ID: t.id, Name: t.name, Args: argsToMap([]byte(t.input.String())),
		}})
	}
	return finalLLMResponse(a.cfg.Model, text, parts)
}

type anAccumTool struct {
	id, name string
	input    strings.Builder
}

func argsToJSON(v any) []byte {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return []byte("{}")
	}
	return b
}

func (a *anthropicLLM) post(ctx context.Context, body anRequest) (*http.Response, error) {
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("x-api-key", a.cfg.APIKey)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("anthropic %s: HTTP %d: %s", a.cfg.Model, resp.StatusCode, readErrBody(resp.Body))
	}
	return resp, nil
}
