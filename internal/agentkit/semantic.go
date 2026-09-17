package agentkit

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// EmbeddingConfig points at an OpenAI-compatible /embeddings endpoint
// (POST {base}/embeddings, Bearer auth, float encoding).
type EmbeddingConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// KnowledgeChunk is one indexed passage of a knowledge file.
type KnowledgeChunk struct {
	ID       uint   `gorm:"primaryKey"`
	File     string `gorm:"index:idx_knowledge_file"`
	ModTime  int64  `gorm:"index:idx_knowledge_file"`
	ChunkIdx int
	Content  string
	Model    string
	Dim      int
	Embedding []byte // little-endian float32 array
}

// KnowledgeIndex is the persistent vector store for workspace knowledge files
// (SQLite next to sessions.db). nil disables semantic search (keyword fallback).
type KnowledgeIndex struct {
	DB *gorm.DB
	mu sync.Mutex // serializes (re)indexing
}

// OpenKnowledgeIndex creates/opens the knowledge index database.
func OpenKnowledgeIndex(path string) (*KnowledgeIndex, error) {
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&KnowledgeChunk{}); err != nil {
		return nil, err
	}
	return &KnowledgeIndex{DB: db}, nil
}

const (
	chunkRunes    = 700 // chunk size in runes
	chunkOverlap  = 100 // overlap between consecutive chunks
	embedBatch    = 32  // inputs per /embeddings request
	semanticTopK   = 8
	semanticWeight = 0.75 // hybrid score: 0.75*cosine + 0.25*keyword
	embedWorkers   = 4    // concurrent /embeddings requests while indexing
)

// SearchOptions carries everything the knowledge tool needs at run time.
type SearchOptions struct {
	Workspace string
	Index     *KnowledgeIndex // nil => keyword only
	Embedding *EmbeddingConfig // nil => keyword only
}

// searchHybrid runs semantic search when possible and blends it with keyword
// scoring; it falls back to pure keyword search when no embedding backend is
// configured or the embedding call fails.
func searchHybrid(opts SearchOptions, query string) (string, error) {
	if opts.Index == nil || opts.Embedding == nil {
		return searchKnowledge(opts.Workspace, query)
	}
	out, err := semanticSearch(opts, query)
	if err != nil {
		// Degrade gracefully rather than failing the model's tool call.
		res, kwErr := searchKnowledge(opts.Workspace, query)
		if kwErr != nil {
			return "", err
		}
		return "（向量检索不可用，已回退关键词：" + err.Error() + "）\n\n" + res, nil
	}
	return out, nil
}

// semanticSearch reindexes stale files, embeds the query and blends cosine
// similarity with keyword hits. A changed embedding model triggers a full
// rebuild, because vectors from different models are not comparable.
func semanticSearch(opts SearchOptions, query string) (string, error) {
	if err := opts.Index.ensureIndexed(opts.Workspace, opts.Embedding); err != nil {
		return "", err
	}
	qvec, err := embedOne(opts.Embedding, query)
	if err != nil {
		return "", err
	}

	var rows []KnowledgeChunk
	if err := opts.Index.DB.Where("model = ?", opts.Embedding.Model).Find(&rows).Error; err != nil {
		return "", err
	}
	var hits []chunkScore
	keywords := strings.Fields(strings.ToLower(query))
	for _, r := range rows {
		vec := decodeVector(r.Embedding, r.Dim)
		if len(vec) == 0 {
			continue
		}
		hits = append(hits, chunkScore{row: r, cos: cosine(qvec, vec), kw: keywordScore(r.Content, keywords)})
	}
	if len(hits) == 0 {
		return "知识库为空：请将 Markdown/文本文件放入工作区 knowledge/ 目录。", nil
	}
	for i := range hits {
		hits[i].kw = min(hits[i].kw, 10) / 10
	}
	// Relevance floor: drop chunks with no meaningful signal so unrelated
	// files never surface just because the corpus is small.
	hits = filterByScore(hits)
	if len(hits) == 0 {
		return "知识库中没有找到与查询相关的内容。", nil
	}
	sort.Slice(hits, func(i, j int) bool {
		return blendScore(hits[i]) > blendScore(hits[j])
	})
	if len(hits) > knowledgeMaxResults {
		hits = hits[:knowledgeMaxResults]
	}
	var sb strings.Builder
	filesSet := map[string]bool{}
	for _, h := range hits {
		fmt.Fprintf(&sb, "### %s（语义相关度 %d%%）\n", h.row.File, int(math.Round(max(h.cos, 0)*100)))
		sb.WriteString(snippetOf(h.row.Content, knowledgeSnippet))
		sb.WriteString("\n\n")
		filesSet[h.row.File] = true
	}
	refs := refsOf(filesSet)
	return withRefs(sb.String(), refs), nil
}

// refsOf returns sorted knowledge/<file> refs for preview integration.
func refsOf(files map[string]bool) []string {
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, knowledgeDirName+"/"+f)
	}
	sort.Strings(out)
	return out
}

// withRefs appends a machine-readable file reference line that the UI uses
// for artifact preview buttons.
func withRefs(text string, refs []string) string {
	if len(refs) == 0 {
		return text
	}
	return strings.TrimSpace(text) + "\n\n文件: " + strings.Join(refs, ", ")
}

// ensureIndexed brings the persistent index in sync with the knowledge dir:
// re-embeds changed files (nested dirs supported, rows keyed by relative
// path), drops removed files, rebuilds everything when the embedding model
// changed.
func (k *KnowledgeIndex) ensureIndexed(workspace string, emb *EmbeddingConfig) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	var staleModels int64
	if err := k.DB.Model(&KnowledgeChunk{}).Where("model <> ?", emb.Model).Count(&staleModels).Error; err != nil {
		return err
	}
	if staleModels > 0 {
		if err := k.DB.Where("1 = 1").Delete(&KnowledgeChunk{}).Error; err != nil {
			return err
		}
	}

	dir := filepath.Join(workspace, knowledgeDirName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil // empty knowledge base; search reports the empty state
	}

	// mtimes of files that still exist (relative paths support nesting).
	present := map[string]int64{}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !isKnowledgeFile(d.Name()) {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() > knowledgeMaxFileKB<<10 {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		present[rel] = st.ModTime().Unix()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	// Drop rows of removed or changed files.
	var known []struct {
		File    string
		ModTime int64
	}
	if err := k.DB.Model(&KnowledgeChunk{}).
		Select("distinct file, mod_time").
		Scan(&known).Error; err != nil {
		return err
	}
	seen := map[string]int64{}
	for _, r := range known {
		seen[r.File] = r.ModTime
	}
	for f := range seen {
		if _, ok := present[f]; !ok {
			if err := k.DB.Where("file = ?", f).Delete(&KnowledgeChunk{}).Error; err != nil {
				return err
			}
		}
	}

	// Collect files that need (re-)embedding.
	names := make([]string, 0, len(present))
	for f := range present {
		if old, ok := seen[f]; !ok || old != present[f] {
			names = append(names, f)
		}
	}
	sort.Strings(names)

	// Fan the embedding work out to a small worker pool (network-bound),
	// then write rows sequentially on this goroutine.
	type embedJob struct {
		file string
		mt   int64
		text []string
		vecs [][]float64
	}
	jobs := make([]embedJob, 0, len(names))
	for _, f := range names {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		jobs = append(jobs, embedJob{file: f, mt: present[f], text: chunkText(string(data))})
	}
	if len(jobs) == 0 {
		return nil
	}
	workers := embedWorkers
	if workers > len(jobs) {
		workers = len(jobs)
	}
	jobsCh := make(chan embedJob)
	results := make(chan embedJob, len(jobs))
	errCh := make(chan error, len(jobs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				vecs, err := embedAll(emb, j.text)
				if err != nil {
					errCh <- fmt.Errorf("embed %s: %w", j.file, err)
					continue
				}
				j.vecs = vecs
				results <- j
			}
		}()
	}
	for _, j := range jobs {
		jobsCh <- j
	}
	close(jobsCh)
	wg.Wait()
	close(results)

	select {
	case err := <-errCh:
		return err
	default:
	}
	for r := range results {
		for i, vec := range r.vecs {
			row := KnowledgeChunk{
				File: r.file, ModTime: r.mt, ChunkIdx: i,
				Content: r.text[i], Model: emb.Model,
				Dim: len(vec), Embedding: encodeVector(vec),
			}
			if err := k.DB.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// embedAll splits chunks into batches, embeds each and concatenates the
// vectors in chunk order.
func embedAll(emb *EmbeddingConfig, chunks []string) ([][]float64, error) {
	out := make([][]float64, 0, len(chunks))
	for i := 0; i < len(chunks); i += embedBatch {
		end := minInt(i+embedBatch, len(chunks))
		vecs, err := embedBatchReq(emb, chunks[i:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Embedding client (OpenAI-compatible /embeddings, float encoding)

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// embedBatchReq embeds a batch of inputs; response embeddings come back in
// input order via the index field.
func embedBatchReq(emb *EmbeddingConfig, inputs []string) ([][]float64, error) {
	body, _ := json.Marshal(embedRequest{Model: emb.Model, Input: inputs})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		strings.TrimRight(emb.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if emb.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+emb.APIKey)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, truncateForLog(data))
	}
	var out embedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings returned %d vectors for %d inputs", len(out.Data), len(inputs))
	}
	vecs := make([][]float64, len(inputs))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("embeddings index %d out of range", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

func embedOne(emb *EmbeddingConfig, input string) ([]float64, error) {
	vecs, err := embedBatchReq(emb, []string{input})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// ---------------------------------------------------------------------------
// Vector + text helpers

func encodeVector(vec []float64) []byte {
	out := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(float32(v)))
	}
	return out
}

func decodeVector(b []byte, dim int) []float64 {
	if dim <= 0 || len(b) != dim*4 {
		return nil
	}
	out := make([]float64, dim)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
	}
	return out
}

// KnowledgeIndexStats summarizes the persistent index for observability.
type KnowledgeIndexStats struct {
	Files  int64  `json:"files"`
	Chunks int64  `json:"chunks"`
	Model  string `json:"model,omitempty"`
}

// Reset wipes all cached chunks; the next search re-embeds everything from
// the workspace files.
func (k *KnowledgeIndex) Reset() error {
	if k == nil || k.DB == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.DB.Where("1 = 1").Delete(&KnowledgeChunk{}).Error
}

// Stats returns file/chunk counts (and the embedding model when indexed).
func (k *KnowledgeIndex) Stats() KnowledgeIndexStats {
	var st KnowledgeIndexStats
	if k == nil || k.DB == nil {
		return st
	}
	k.DB.Model(&KnowledgeChunk{}).Count(&st.Chunks)
	k.DB.Model(&KnowledgeChunk{}).Distinct("file").Count(&st.Files)
	k.DB.Model(&KnowledgeChunk{}).Distinct("model").Pluck("model", &st.Model)
	return st
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// chunkText splits content into overlapping rune windows, preferring line
// boundaries so chunks stay readable.
func chunkText(s string) []string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return nil
	}
	var out []string
	for start := 0; start < len(runes); {
		end := minInt(start+chunkRunes, len(runes))
		if end < len(runes) { // prefer breaking at a newline past the midpoint
			for i := end - 1; i > start+chunkRunes/2; i-- {
				if runes[i] == '\n' {
					end = i + 1
					break
				}
			}
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
		if end >= len(runes) {
			break
		}
		next := end - chunkOverlap
		if next <= start { // guard forward progress
			next = start + chunkRunes - chunkOverlap
		}
		start = next
	}
	return out
}

func keywordScore(content string, keywords []string) float64 {
	lower := strings.ToLower(content)
	score := 0.0
	for _, kw := range keywords {
		score += float64(strings.Count(lower, kw))
	}
	return score
}

// chunkScore couples an indexed chunk with its similarity signals.
type chunkScore struct {
	row KnowledgeChunk
	cos float64
	kw  float64
}

// blendScore mixes semantic and keyword signals into one ranking score.
func blendScore(h chunkScore) float64 { return semanticWeight*h.cos + 0.25*h.kw }

// filterByScore keeps chunks whose blended semantic+keyword score clears a
// small relevance floor.
func filterByScore(hits []chunkScore) []chunkScore {
	out := hits[:0]
	for _, h := range hits {
		if blendScore(h) >= 0.05 {
			out = append(out, h)
		}
	}
	return out
}

func snippetOf(content string, maxRunes int) string {
	content = strings.TrimSpace(content)
	r := []rune(content)
	if len(r) <= maxRunes {
		return content
	}
	return string(r[:maxRunes]) + "…"
}

func truncateForLog(b []byte) string {
	s := string(b)
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	return strings.ReplaceAll(string(r), "\n", " ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

