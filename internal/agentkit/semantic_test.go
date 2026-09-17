package agentkit

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/config"
)

// fakeEmbeddings serves the OpenAI-compatible /embeddings contract with
// deterministic bag-of-words hashing vectors: shared vocabulary => high
// cosine similarity, which is enough to exercise ranking, caching and
// rebuild paths without a real model.
func fakeEmbeddings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type emb struct {
			Object    string    `json:"object"`
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
			sq := sqrtApprox(sumsq)
			if sq == 0 {
				sq = 1
			}
			for j := range vec {
				vec[j] /= sq
			}
			data[i] = emb{Object: "embedding", Index: i, Embedding: vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"model":  "fake-embed",
			"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}
}

func sqrtApprox(x float64) float64 {
	// Newton's method; the vectors are small so precision is irrelevant.
	if x == 0 {
		return 0
	}
	g := x
	for i := 0; i < 40; i++ {
		g = (g + x/g) / 2
	}
	return g
}

func seedKnowledge(t *testing.T, files map[string]string) string {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func TestSemanticSearchRanksAndCaches(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()

	ws := seedKnowledge(t, map[string]string{
		"k8s.md":     "# 集群\n\nkubernetes pod 调度策略与资源配额管理是集群稳定性的关键。",
		"cooking.md": "# 烹饪\n\n红烧肉的做法需要先焯水，再加入冰糖炒出糖色。",
	})
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	emb := &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: emb}

	// Count embedding inputs by wrapping the server: simpler to assert on
	// stored rows after searches.
	res, err := searchHybrid(opts, "kubernetes pod 调度")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "语义相关度") {
		t.Fatalf("expected semantic output, got: %s", res)
	}
	if !strings.HasPrefix(strings.TrimSpace(res), "### k8s.md") {
		t.Fatalf("expected k8s.md ranked first, got: %s", res)
	}
	if strings.Contains(res, "红烧肉") {
		t.Fatalf("irrelevant file leaked into results: %s", res)
	}

	// Indexed rows exist and a second search reuses them (no re-embed of
	// unchanged files): row count stable and search still works.
	var rows int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Count(&rows).Error; err != nil || rows == 0 {
		t.Fatalf("expected indexed rows, rows=%d err=%v", rows, err)
	}
	before := rowCount(t, idx)
	if _, err := searchHybrid(opts, "kubernetes pod 调度"); err != nil {
		t.Fatal(err)
	}
	if after := rowCount(t, idx); after != before {
		t.Fatalf("unchanged files must not re-index: %d -> %d", before, after)
	}

	// Changing a file re-indexes just that file.
	if err := os.WriteFile(filepath.Join(ws, "knowledge", "cooking.md"),
		[]byte("# 烹饪\n\n新版本：红烧肉做法，加豆腐乳更香。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := searchHybrid(opts, "红烧肉 豆腐乳"); err != nil {
		t.Fatal(err)
	}
	if after := rowCount(t, idx); after != before {
		t.Fatalf("same total chunks expected after edit, %d -> %d", before, after)
	}
	var mod int64
	idx.DB.Model(&KnowledgeChunk{}).Where("file = ?", "cooking.md").
		Select("count(*)").Scan(&mod)
	if mod == 0 {
		t.Fatal("cooking.md rows missing after edit")
	}
}

func rowCount(t *testing.T, idx *KnowledgeIndex) int64 {
	t.Helper()
	var n int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSemanticModelChangeRebuilds(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "向量检索测试内容 vector search"})
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "model-a"}}
	if _, err := searchHybrid(opts, "向量检索"); err != nil {
		t.Fatal(err)
	}
	first := rowCount(t, idx)

	// Switching models wipes and rebuilds: rows carry the new model only.
	opts.Embedding.Model = "model-b"
	if _, err := searchHybrid(opts, "向量检索"); err != nil {
		t.Fatal(err)
	}
	if total := rowCount(t, idx); total != first {
		t.Fatalf("rebuild should keep chunk count stable: %d vs %d", first, total)
	}
	var oldModel int64
	idx.DB.Model(&KnowledgeChunk{}).Where("model = ?", "model-a").Count(&oldModel)
	if oldModel != 0 {
		t.Fatalf("stale model rows must be purged, got %d", oldModel)
	}
}

func TestSemanticFallbacks(t *testing.T) {
	ws := seedKnowledge(t, map[string]string{"a.md": "部署手册 deploy guide\n\n部署 deploy 步骤说明。"})

	// No embedding configured: pure keyword behaviour, no marker.
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := searchHybrid(SearchOptions{Workspace: ws, Index: idx}, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "向量检索不可用") || strings.Contains(res, "语义相关度") {
		t.Fatalf("expected keyword-only output, got: %s", res)
	}

	// Embedding backend error: graceful keyword fallback with a note.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	res, err = searchHybrid(SearchOptions{
		Workspace: ws, Index: idx,
		Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "m"},
	}, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "向量检索不可用") || !strings.Contains(res, "deploy") {
		t.Fatalf("expected fallback with note, got: %s", res)
	}
}

func TestChunkText(t *testing.T) {
	long := strings.Repeat("行内容\n", 500) // 1500 runes
	chunks := chunkText(long)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > chunkRunes {
			t.Fatalf("chunk %d too large: %d", i, n)
		}
		if strings.TrimSpace(c) == "" {
			t.Fatalf("chunk %d empty", i)
		}
	}
	if got := chunkText("短文本"); len(got) != 1 || got[0] != "短文本" {
		t.Fatalf("small text should be one chunk: %v", got)
	}
	if got := chunkText(""); got != nil {
		t.Fatalf("empty text should yield no chunks: %v", got)
	}
}

// TestEmbeddingsProtocolContract pins the request/response handling to the
// OpenAI shape: batch order restored via the index field, auth header set.
func TestEmbeddingsProtocolContract(t *testing.T) {
	var gotAuth, gotPath string
	var gotModel string
	var gotInputs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		gotInputs = len(req.Input)
		// Return out of order on purpose; client must restore by index.
		fmt.Fprint(w, `{"object":"list","data":[`+
			`{"object":"embedding","index":1,"embedding":[0.4,0.0]},`+
			`{"object":"embedding","index":0,"embedding":[0.0,1.0]}],"model":"m","usage":{}}`)
	}))
	defer srv.Close()

	emb := &EmbeddingConfig{BaseURL: srv.URL, APIKey: "sk-test", Model: "m"}
	vecs, err := embedBatchReq(emb, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/embeddings" || gotAuth != "Bearer sk-test" || gotModel != "m" || gotInputs != 2 {
		t.Fatalf("request contract broken: path=%s auth=%s model=%s inputs=%d", gotPath, gotAuth, gotModel, gotInputs)
	}
	if vecs[0][1] != 1.0 || vecs[1][0] != 0.4 {
		t.Fatalf("vectors not restored by index: %v", vecs)
	}
}

func TestKnowledgeIndexStats(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{
		"a.md": "alpha 文件内容 one two",
		"b.md": "beta 文件内容 three",
	})
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	if _, err := searchHybrid(opts, "alpha"); err != nil {
		t.Fatal(err)
	}
	st := idx.Stats()
	if st.Files != 2 || st.Chunks < 2 || st.Model != "fake-embed" {
		t.Fatalf("stats wrong: %+v", st)
	}
	var nilIndex *KnowledgeIndex
	zero := nilIndex.Stats()
	if zero.Chunks != 0 {
		t.Fatalf("nil index stats must be zero: %+v", zero)
	}
}

func TestKnowledgeIndexReset(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "内容 content here"})
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	if _, err := searchHybrid(opts, "内容"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, idx) == 0 {
		t.Fatal("expected rows before reset")
	}
	if err := idx.Reset(); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, idx); n != 0 {
		t.Fatalf("reset must wipe rows, got %d", n)
	}
	// Next search rebuilds automatically.
	if _, err := searchHybrid(opts, "内容"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, idx) == 0 {
		t.Fatal("index did not rebuild after reset")
	}
}

// TestKitTestKnowledgeSearch: the settings-page debugger path resolves
// workspace + embedding settings through the Kit and returns search output.
func TestKitTestKnowledgeSearch(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "alpha 内容 here"})
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = ws
		cfg.Settings.EmbeddingProviderID = "p"
		cfg.Settings.EmbeddingModel = "fake-embed"
		cfg.Providers = append(cfg.Providers, config.ModelProvider{ID: "p", Name: "P", Protocol: config.ProtocolOpenAI, BaseURL: srv.URL})
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	kit := NewKit(store, nil)
	kit.SetKnowledgeIndex(idx)

	res, err := kit.TestKnowledgeSearch("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "语义相关度") {
		t.Fatalf("expected semantic output: %s", res)
	}
	if _, err := kit.TestKnowledgeSearch("   "); err == nil {
		t.Fatal("empty query should error")
	}
}
