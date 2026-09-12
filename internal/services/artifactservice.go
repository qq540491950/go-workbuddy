package services

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"changeme/internal/agentkit"
)

const (
	maxBinaryArtifact = 12 << 20 // 12 MB for image/pdf preview
	maxTextArtifact   = 512 << 10 // 512 KB for text preview
)

// Artifact is a previewable file from the workspace.
type Artifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"` // relative to the workspace
	Kind      string `json:"kind"` // image|markdown|text|pdf|binary
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
	DataURL   string `json:"dataUrl,omitempty"` // image/pdf (base64 data URL)
	Text      string `json:"text,omitempty"`    // text/markdown/code
	Truncated bool   `json:"truncated"`
}

// ArtifactInfo is a lightweight listing entry.
type ArtifactInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

// ArtifactService previews workspace files produced by tool calls.
type ArtifactService struct{ S *Services }

// NewArtifactService creates the service.
func NewArtifactService(s *Services) *ArtifactService { return &ArtifactService{S: s} }

var artifactMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
	".bmp": "image/bmp", ".ico": "image/x-icon",
	".pdf": "application/pdf",
	".md":  "text/markdown", ".markdown": "text/markdown",
	".txt": "text/plain", ".log": "text/plain", ".csv": "text/csv",
	".json": "application/json", ".yaml": "text/yaml", ".yml": "text/yaml",
	".xml": "application/xml", ".html": "text/html", ".htm": "text/html",
	".go": "text/x-go", ".ts": "text/typescript", ".tsx": "text/typescript",
	".js": "text/javascript", ".jsx": "text/javascript", ".py": "text/x-python",
	".rs": "text/x-rust", ".java": "text/x-java", ".c": "text/x-c",
	".h": "text/x-c", ".cpp": "text/x-c++", ".sh": "text/x-shellscript",
	".sql": "text/x-sql", ".toml": "text/plain", ".ini": "text/plain",
	".css": "text/css", ".wasm": "application/wasm",
}

func artifactKind(ext string) string {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico":
		return "image"
	case ".pdf":
		return "pdf"
	case ".md", ".markdown":
		return "markdown"
	}
	return "text"
}

// workspace resolves the configured workspace directory.
func (a *ArtifactService) workspace() string { return a.S.Store.Get().Settings.WorkspaceDir }

// Open reads a workspace file for preview. Path is relative to the workspace.
func (a *ArtifactService) Open(relPath string) (*Artifact, error) {
	ws := a.workspace()
	if strings.TrimSpace(ws) == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	abs, err := agentkit.SafeWorkspacePath(ws, relPath)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%q 是目录，仅支持预览文件", relPath)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	mime := artifactMIME[ext]
	if mime == "" {
		mime = "application/octet-stream"
	}
	art := &Artifact{
		Name: filepath.Base(abs),
		Path: filepath.ToSlash(relPath),
		MIME: mime,
		Size: st.Size(),
	}
	switch kind := artifactKind(ext); kind {
	case "image", "pdf":
		if st.Size() > maxBinaryArtifact {
			return nil, fmt.Errorf("文件超过预览上限（%d MB）", maxBinaryArtifact>>20)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		art.Kind = kind
		art.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return art, nil
	default:
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		art.Kind = kind
		if int64(len(data)) > maxTextArtifact {
			data = data[:maxTextArtifact]
			art.Truncated = true
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("仅支持预览文本文件（%s 是二进制文件）", art.Name)
		}
		art.Text = string(data)
		return art, nil
	}
}

// List returns previewable files under the workspace (newest first, capped).
func (a *ArtifactService) List() ([]ArtifactInfo, error) {
	ws := a.workspace()
	if ws == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	var out []ArtifactInfo
	maxItems := 200
	_ = filepath.WalkDir(ws, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(path, "/exports/") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if artifactMIME[ext] == "" {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return nil
		}
		out = append(out, ArtifactInfo{
			Name: d.Name(), Path: filepath.ToSlash(rel),
			Kind: artifactKind(ext), Size: st.Size(),
		})
		if len(out) >= maxItems {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Workspace returns the configured workspace directory (for UI display).
func (a *ArtifactService) Workspace() string { return a.workspace() }
