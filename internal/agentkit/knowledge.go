package agentkit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

const (
	knowledgeDirName      = "knowledge"
	knowledgeMaxResults   = 6
	knowledgeSnippet      = 240
	knowledgeMaxFileKB    = 512
	knowledgeMaxScanFiles = 200 // keyword search caps full-text scans per call
)

type queryArgs struct {
	Query string `json:"query" jsonschema:"search keywords, space separated"`
}

// filesResult is the search_knowledge payload; Refs carries
// workspace-relative paths so the UI can offer artifact previews.
type filesResult struct {
	Entries string   `json:"entries"`
	Refs    []string `json:"refs,omitempty"`
}

// SearchKnowledgeTool searches the workspace knowledge base: hybrid
// semantic+keyword when an embedding backend is wired in, keyword otherwise.
func SearchKnowledgeTool(opts SearchOptions) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "search_knowledge",
		Description: "Search the user's knowledge base (markdown/text files under the workspace 'knowledge' folder). Returns the most relevant file excerpts.",
	}, func(ctx agent.Context, args queryArgs) (filesResult, error) {
		if strings.TrimSpace(args.Query) == "" {
			return filesResult{}, os.ErrInvalid
		}
		// The run context bounds embedding HTTP calls: a cancelled or
		// timed-out turn stops in-flight indexing instead of holding the
		// index lock behind context.Background().
		o := opts
		o.Ctx = ctx
		res, err := searchHybrid(o, args.Query)
		if err != nil {
			return filesResult{}, err
		}
		return filesResult{Entries: res}, nil
	})
}

// searchKnowledge scans knowledge files and ranks them by keyword hits.
func searchKnowledge(workspace, query string) (string, error) {
	if workspace == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(workspace, knowledgeDirName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "知识库为空：请将 Markdown/文本文件放入工作区 knowledge/ 目录。", nil
	}

	keywords := strings.Fields(strings.ToLower(query))
	type hit struct {
		name  string
		score int
		snip  string
	}
	// Keyword search re-reads every file on every call (the vector path has
	// mtime-incremental indexing; this one does not), so an unbounded corpus
	// would read hundreds of MB per tool call. Collect candidates first, then
	// full-text scan only the knowledgeMaxScanFiles most recently modified.
	type candidate struct {
		path string
		rel  string
		mt   int64
	}
	var candidates []candidate
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
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
			rel = d.Name()
		}
		candidates = append(candidates, candidate{path: path, rel: rel, mt: st.ModTime().Unix()})
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mt > candidates[j].mt })
	if len(candidates) > knowledgeMaxScanFiles {
		candidates = candidates[:knowledgeMaxScanFiles]
	}
	var hits []hit
	for _, c := range candidates {
		data, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if score == 0 {
			continue
		}
		hits = append(hits, hit{name: c.rel, score: score, snip: bestSnippet(string(data), keywords)})
	}
	if len(hits) == 0 {
		return "知识库中没有找到与查询相关的内容。", nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > knowledgeMaxResults {
		hits = hits[:knowledgeMaxResults]
	}
	var sb strings.Builder
	files := map[string]bool{}
	for _, h := range hits {
		sb.WriteString("### " + h.name + "（相关度 " + itoa(h.score) + "）\n")
		sb.WriteString(h.snip + "\n\n")
		files[h.name] = true
	}
	sb.WriteString(withRefs("", refsOf(files)))
	return strings.TrimSpace(sb.String()), nil
}

func bestSnippet(content string, keywords []string) string {
	lines := strings.Split(content, "\n")
	type lineHit struct {
		text  string
		score int
	}
	var best []lineHit
	for _, ln := range lines {
		lower := strings.ToLower(ln)
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if score > 0 {
			best = append(best, lineHit{ln, score})
		}
	}
	sort.Slice(best, func(i, j int) bool { return best[i].score > best[j].score })
	var picked []string
	total := 0
	for _, l := range best {
		t := strings.TrimSpace(l.text)
		if t == "" {
			continue
		}
		if len([]rune(t)) > knowledgeSnippet {
			t = string([]rune(t)[:knowledgeSnippet]) + "…"
		}
		picked = append(picked, "- "+t)
		total++
		if total >= 5 {
			break
		}
	}
	if len(picked) == 0 {
		return "（匹配但无可用摘要）"
	}
	return strings.Join(picked, "\n")
}

func isKnowledgeFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".txt", ".csv", ".json":
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
