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
	knowledgeDirName    = "knowledge"
	knowledgeMaxResults = 6
	knowledgeSnippet    = 240
	knowledgeMaxFileKB  = 512
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
func SearchKnowledgeTool(opts SearchOptions) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "search_knowledge",
		Description: "Search the user's knowledge base (markdown/text files under the workspace 'knowledge' folder). Returns the most relevant file excerpts.",
	}, func(ctx agent.Context, args queryArgs) (filesResult, error) {
		if strings.TrimSpace(args.Query) == "" {
			return filesResult{}, os.ErrInvalid
		}
		res, err := searchHybrid(opts, args.Query)
		if err != nil {
			return filesResult{}, err
		}
		return filesResult{Entries: res}, nil
	})
	return t
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
	var hits []hit
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		if !isKnowledgeFile(d.Name()) {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() > knowledgeMaxFileKB<<10 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = d.Name()
		}
		lower := strings.ToLower(string(data))
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if score == 0 {
			return nil
		}
		hits = append(hits, hit{name: rel, score: score, snip: bestSnippet(string(data), keywords)})
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if len(hits) == 0 {
		return "知识库中没有找到与查询相关的内容。", nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > knowledgeMaxResults {
		hits = hits[:knowledgeMaxResults]
	}
	var sb strings.Builder
	for _, h := range hits {
		sb.WriteString("### " + h.name + "（相关度 " + itoa(h.score) + "）\n")
		sb.WriteString(h.snip + "\n\n")
	}
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
