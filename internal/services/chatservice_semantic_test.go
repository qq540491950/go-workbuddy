package services

import (
	"encoding/json"
	"hash/fnv"
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

// fakeEmbeddingsHandler speaks the OpenAI-compatible /embeddings contract with
// deterministic bag-of-words vectors (same trick as the agentkit unit tests).
func fakeEmbeddingsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type emb struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}
		data := make([]emb, len(req.Input))
		for i, in := range req.Input {
			vec := make([]float64, 64)
			for _, w := range strings.Fields(strings.ToLower(in)) {
				h := fnv.New32a()
				_, _ = h.Write([]byte(w))
				vec[h.Sum32()%64] += 1
			}
			var sumsq float64
			for _, v := range vec {
				sumsq += v * v
			}
			sq := 1.0
			if sumsq > 0 {
				sq = sumsq
				for j := 0; j < 40; j++ {
					sq = (sq + sumsq/sq) / 2
				}
			}
			for j := range vec {
				vec[j] /= sq
			}
			data[i] = emb{Index: i, Embedding: vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "data": data, "model": "fake-embed", "usage": map[string]any{},
		})
	}
}

// TestChatSemanticKnowledgeE2E wires an embedding provider into settings and
// exercises the full loop: ChatService.Send -> search_knowledge tool -> hybrid
// semantic retrieval against the workspace knowledge directory.
func TestChatSemanticKnowledgeE2E(t *testing.T) {
	embedSrv := httptest.NewServer(fakeEmbeddingsHandler())
	defer embedSrv.Close()

	turn := 0
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "search_knowledge", `"{\"query\":\"kubernetes pod 调度\"}"`) + sseDone))
		} else {
			if !strings.Contains(readAll(r), "语义相关度") {
				t.Errorf("model prompt missing semantic search result")
			}
			_, _ = w.Write([]byte(sseText("知识库里说调度策略很关键。") + sseDone))
		}
	}))
	defer chatSrv.Close()

	dir := t.TempDir()
	workspace := t.TempDir()
	kb := filepath.Join(workspace, "knowledge")
	if err := os.MkdirAll(kb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "k8s.md"),
		[]byte("kubernetes pod 调度策略与资源配额管理是集群稳定性的关键。"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Settings.EmbeddingProviderID = "prov_emb"
		cfg.Settings.EmbeddingModel = "fake-embed"
		cfg.Providers = append(cfg.Providers,
			config.ModelProvider{
				ID: "prov_chat", Name: "Chat", Protocol: config.ProtocolOpenAI,
				BaseURL: chatSrv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
			},
			config.ModelProvider{
				ID: "prov_emb", Name: "Embed", Protocol: config.ProtocolOpenAI,
				BaseURL: embedSrv.URL, Models: []config.ModelInfo{{ID: "fake-embed"}},
			},
		)
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_k", Name: "知识助手", ProviderID: "prov_chat", Model: "fake-mini",
			BuiltinTools: []string{"knowledge"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	idx, err := agentkit.OpenKnowledgeIndex(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	svc.Kit.SetKnowledgeIndex(idx)
	chat := NewChatService(svc, sessions, db)

	sess, err := chat.NewSession("agent", "agent_k", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "知识库里怎么看调度？", nil); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawToolResult bool
	for _, m := range msgs {
		if m.Kind == "tool_result" && m.ToolName == "search_knowledge" &&
			strings.Contains(string(mustJSON(m.ToolResp)), "语义相关度") {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("semantic tool result missing from history: %+v", msgs)
	}

	// The index persisted and the model saw the retrieval.
	var rows int64
	idx.DB.Model(&agentkit.KnowledgeChunk{}).Count(&rows)
	if rows == 0 {
		t.Fatal("knowledge index was not populated")
	}
}
