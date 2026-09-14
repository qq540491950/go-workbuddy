package agentkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"changeme/internal/config"
)

func TestListOpenAIModels(t *testing.T) {
	var gotAuth string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "deepseek-reasoner"},
				{"id": "deepseek-chat"},
				{"id": ""}, // filtered out
			},
		})
	}))
	defer srv.Close()

	ids, err := ListProviderModels(context.Background(), config.ModelProvider{
		Protocol: config.ProtocolOpenAI, BaseURL: srv.URL, APIKey: "sk-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/models" {
		t.Fatalf("path = %q, want /models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if strings.Join(ids, ",") != "deepseek-chat,deepseek-reasoner" {
		t.Fatalf("ids = %v, want sorted unique", ids)
	}
}

func TestListOpenAIModelsDefaultBaseURL(t *testing.T) {
	_, err := ListProviderModels(context.Background(), config.ModelProvider{Protocol: config.ProtocolOpenAI})
	if err == nil {
		t.Fatal("expected request against api.openai.com to fail in sandbox, got nil")
	}
}

func TestListAnthropicModelsPagination(t *testing.T) {
	pages := []map[string]any{
		{"data": []map[string]any{{"id": "claude-sonnet-4"}}, "has_more": true, "next_page": "p2"},
		{"data": []map[string]any{{"id": "claude-opus-4"}, {"id": "claude-sonnet-4"}}, "has_more": false},
	}
	page := 0
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if page >= len(pages) {
			t.Fatal("requested more pages than provided")
		}
		json.NewEncoder(w).Encode(pages[page])
		page++
	}))
	defer srv.Close()

	ids, err := ListProviderModels(context.Background(), config.ModelProvider{
		Protocol: config.ProtocolAnthropic, BaseURL: srv.URL, APIKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page != 2 {
		t.Fatalf("fetched %d pages, want 2", page)
	}
	if gotKey != "key-1" || gotVersion != "2023-06-01" {
		t.Fatalf("headers = %q / %q", gotKey, gotVersion)
	}
	if strings.Join(ids, ",") != "claude-opus-4,claude-sonnet-4" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestListGeminiModelsFiltersGenerateContent(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		if r.URL.Path != "/v1beta/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-2.5-flash", "supportedGenerationMethods": []string{"generateContent", "countTokens"}},
				{"name": "models/text-embedding-004", "supportedGenerationMethods": []string{"embedContent"}},
			},
		})
	}))
	defer srv.Close()

	ids, err := ListProviderModels(context.Background(), config.ModelProvider{
		Protocol: config.ProtocolGemini, BaseURL: srv.URL, APIKey: "g-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "g-key" {
		t.Fatalf("key = %q", gotKey)
	}
	if strings.Join(ids, ",") != "gemini-2.5-flash" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestListModelsHTTPErrorCarriesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	_, err := ListProviderModels(context.Background(), config.ModelProvider{
		Protocol: config.ProtocolOpenAI, BaseURL: srv.URL, APIKey: "bad",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("err = %v, want body message", err)
	}
}
