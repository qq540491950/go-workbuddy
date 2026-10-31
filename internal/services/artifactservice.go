package services

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"changeme/internal/agentkit"
)

const (
	maxBinaryArtifact = 12 << 20  // 12 MB for image/pdf preview
	maxTextArtifact   = 512 << 10 // 512 KB for text preview
	maxSnapshotFiles  = 5000
	snapshotListLimit = 200
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

// WorkspaceFileInfo is one file in a task workspace snapshot.
type WorkspaceFileInfo struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Kind        string    `json:"kind"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modTime"`
	Previewable bool      `json:"previewable"`
	Status      string    `json:"status,omitempty"` // added|modified|deleted
}

// TaskWorkspaceSnapshot is the result pane contract for one chat task.
type TaskWorkspaceSnapshot struct {
	SessionID   string              `json:"sessionId"`
	Workspace   string              `json:"workspace"`
	Files       []WorkspaceFileInfo `json:"files"`
	Artifacts   []WorkspaceFileInfo `json:"artifacts"`
	Changes     []WorkspaceFileInfo `json:"changes"`
	Discovered  int                 `json:"discovered"`
	Added       int                 `json:"added"`
	Modified    int                 `json:"modified"`
	Deleted     int                 `json:"deleted"`
	Truncated   bool                `json:"truncated"`
	BaselineAt  *time.Time          `json:"baselineAt,omitempty"`
	RefreshedAt time.Time           `json:"refreshedAt"`
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
	return a.openIn(a.workspace(), relPath)
}

// OpenForSession previews a file inside the task workspace bound to sessionID.
func (a *ArtifactService) OpenForSession(sessionID, relPath string) (*Artifact, error) {
	ws, err := a.sessionWorkspace(sessionID)
	if err != nil {
		return nil, err
	}
	return a.openIn(ws, relPath)
}

func (a *ArtifactService) sessionWorkspace(sessionID string) (string, error) {
	cfg := a.S.Store.Get()
	for _, session := range cfg.Sessions {
		if session.ID == sessionID {
			return session.EffectiveWorkspace(cfg.Settings.WorkspaceDir), nil
		}
	}
	return "", fmt.Errorf("会话 %q 不存在", sessionID)
}

func (a *ArtifactService) openIn(ws, relPath string) (*Artifact, error) {
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
		art.Kind = kind
		text, truncated, err := agentkit.ReadBoundedFile(abs, maxTextArtifact)
		if err != nil {
			return nil, err
		}
		art.Truncated = truncated
		if !utf8.ValidString(text) {
			return nil, fmt.Errorf("仅支持预览文本文件（%s 是二进制文件）", art.Name)
		}
		art.Text = text
		return art, nil
	}
}

// TaskWorkspaceSnapshot lists the active task's files. Artifacts and changes
// are filled by the baseline-aware refresh path; the initial file browser
// still returns the complete task view without blocking on baseline state.
func (a *ArtifactService) TaskWorkspaceSnapshot(sessionID string) (*TaskWorkspaceSnapshot, error) {
	ws, err := a.sessionWorkspace(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ws) == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	if st, err := os.Stat(ws); err != nil {
		return nil, fmt.Errorf("任务工作区不可用: %w", err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("任务工作区必须是目录")
	}
	files, discovered, truncated, err := scanTaskFiles(ws)
	if err != nil {
		return nil, err
	}
	if len(files) > snapshotListLimit {
		files = files[:snapshotListLimit]
		truncated = true
	}
	return &TaskWorkspaceSnapshot{
		SessionID:   sessionID,
		Workspace:   ws,
		Files:       files,
		Artifacts:   []WorkspaceFileInfo{},
		Changes:     []WorkspaceFileInfo{},
		Discovered:  discovered,
		Truncated:   truncated,
		RefreshedAt: time.Now().UTC(),
	}, nil
}

var skippedWorkspaceDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, ".pnpm-store": true, ".yarn": true,
	".venv": true, "venv": true, "__pycache__": true,
	".cache": true, ".next": true,
}

func scanTaskFiles(ws string) ([]WorkspaceFileInfo, int, bool, error) {
	var files []WorkspaceFileInfo
	discovered := 0
	truncated := false
	err := filepath.WalkDir(ws, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == ws {
				return walkErr
			}
			return nil
		}
		if path != ws && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != ws && skippedWorkspaceDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == "exports" || strings.HasPrefix(relSlash, "exports/") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		discovered++
		if len(files) >= maxSnapshotFiles {
			truncated = true
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		kind, previewable := snapshotKind(ext)
		files = append(files, WorkspaceFileInfo{
			Name:        d.Name(),
			Path:        relSlash,
			Kind:        kind,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Previewable: previewable,
		})
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ModTime.After(files[j].ModTime)
		}
		return files[i].Path < files[j].Path
	})
	return files, discovered, truncated, nil
}

func snapshotKind(ext string) (string, bool) {
	mime := artifactMIME[ext]
	if mime == "" || mime == "application/wasm" {
		return "binary", false
	}
	switch artifactKind(ext) {
	case "image":
		return "image", true
	case "pdf":
		return "pdf", true
	case "markdown":
		return "markdown", true
	default:
		return "text", true
	}
}

// List returns previewable files under the workspace (newest first, capped).
func (a *ArtifactService) List() ([]ArtifactInfo, error) {
	ws := a.workspace()
	if ws == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	type found struct {
		info ArtifactInfo
		mt   int64
	}
	var foundFiles []found
	_ = filepath.WalkDir(ws, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// ToSlash: WalkDir emits OS separators, and the exports filter must
		// hold on Windows too.
		if strings.Contains(filepath.ToSlash(path), "/exports/") || strings.HasPrefix(d.Name(), ".") {
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
		foundFiles = append(foundFiles, found{
			info: ArtifactInfo{
				Name: d.Name(), Path: filepath.ToSlash(rel),
				Kind: artifactKind(ext), Size: st.Size(),
			},
			mt: st.ModTime().UnixNano(),
		})
		return nil
	})
	// The doc promises newest first: sort by mtime BEFORE capping, otherwise
	// the lexical walk order decides which files survive the cap. Same-mtime
	// files (bulk writes land in one timestamp tick) tie-break by path so the
	// order stays deterministic.
	sort.Slice(foundFiles, func(i, j int) bool {
		if foundFiles[i].mt != foundFiles[j].mt {
			return foundFiles[i].mt > foundFiles[j].mt
		}
		return foundFiles[i].info.Path < foundFiles[j].info.Path
	})
	const maxItems = 200
	if len(foundFiles) > maxItems {
		foundFiles = foundFiles[:maxItems]
	}
	out := make([]ArtifactInfo, 0, len(foundFiles))
	for _, f := range foundFiles {
		out = append(out, f.info)
	}
	return out, nil
}

// Workspace returns the configured workspace directory (for UI display).
func (a *ArtifactService) Workspace() string { return a.workspace() }
