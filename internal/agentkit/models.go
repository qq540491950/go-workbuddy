// This file fetches the model catalog from a provider's list endpoint, so
// the settings UI can offer a picker instead of hand-typed model ids.
package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"changeme/internal/config"
)

// ListProviderModels fetches the model IDs offered by the provider, using
// the list endpoint of its protocol:
//   - OpenAI-compatible: GET {base}/models
//   - Anthropic:         GET {base}/v1/models (paginated)
//   - Gemini:            GET {base}/v1beta/models (generateContent only)
//
// The result is deduplicated and sorted. Gateways that do not implement the
// list endpoint return an error; callers should keep manual entry available.
func ListProviderModels(ctx context.Context, p config.ModelProvider) ([]string, error) {
	switch p.Protocol {
	case config.ProtocolAnthropic:
		return listAnthropicModels(ctx, p)
	case config.ProtocolGemini:
		return listGeminiModels(ctx, p)
	default: // OpenAI-compatible Chat Completions
		return listOpenAIModels(ctx, p)
	}
}

func httpGetJSON(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrBody(resp.Body))
	}
	return resp, nil
}

func decodeJSON(body io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(body, 8*1024*1024)).Decode(v)
}

// listOpenAIModels speaks GET /models (OpenAI, DeepSeek, Kimi, Ollama, vLLM...).
func listOpenAIModels(ctx context.Context, p config.ModelProvider) ([]string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	headers := map[string]string{}
	if p.APIKey != "" {
		headers["Authorization"] = "Bearer " + p.APIKey
	}
	resp, err := httpGetJSON(ctx, base+"/models", headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := decodeJSON(resp.Body, &out); err != nil {
		return nil, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("%s", out.Error.Message)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// listAnthropicModels speaks GET /v1/models with cursor pagination.
func listAnthropicModels(ctx context.Context, p config.ModelProvider) ([]string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if p.APIKey != "" {
		headers["x-api-key"] = p.APIKey
	}
	seen := map[string]bool{}
	var ids []string
	cursor := ""
	for page := 0; page < 10; page++ {
		url := base + "/v1/models?limit=1000"
		if cursor != "" {
			url += "&page=" + cursor
		}
		resp, err := httpGetJSON(ctx, url, headers)
		if err != nil {
			return nil, err
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore  bool   `json:"has_more"`
			NextPage string `json:"next_page"`
		}
		err = decodeJSON(resp.Body, &out)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, m := range out.Data {
			if m.ID != "" && !seen[m.ID] {
				seen[m.ID] = true
				ids = append(ids, m.ID)
			}
		}
		if !out.HasMore || out.NextPage == "" {
			break
		}
		cursor = out.NextPage
	}
	sort.Strings(ids)
	return ids, nil
}

// listGeminiModels speaks GET /v1beta/models, keeping only models that
// support generateContent (chat), and strips the "models/" prefix.
func listGeminiModels(ctx context.Context, p config.ModelProvider) ([]string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	url := base + "/v1beta/models?pageSize=200"
	if p.APIKey != "" {
		url += "&key=" + p.APIKey
	}
	resp, err := httpGetJSON(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Models []struct {
			Name      string   `json:"name"` // "models/gemini-2.5-flash"
			Methods   []string `json:"supportedGenerationMethods"`
			NextToken string   `json:"nextPageToken"`
		} `json:"models"`
	}
	if err := decodeJSON(resp.Body, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if !supportsGenerateContent(m.Methods) {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func supportsGenerateContent(methods []string) bool {
	for _, m := range methods {
		if m == "generateContent" {
			return true
		}
	}
	return false
}
