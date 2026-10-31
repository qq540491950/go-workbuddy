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

// WorkspaceChangeStatus is the file-level state shown in the result pane.
type WorkspaceChangeStatus string

const (
	ChangeAdded    WorkspaceChangeStatus = "added"
	ChangeModified WorkspaceChangeStatus = "modified"
	ChangeDeleted  WorkspaceChangeStatus = "deleted"
)

// WorkspaceFileInfo is one file in a task workspace snapshot.
type WorkspaceFileInfo struct {
	Name        string                `json:"name"`
	Path        string                `json:"path"`
	Kind        string                `json:"kind"`
	Size        int64                 `json:"size"`
	ModTime     time.Time             `json:"modTime"`
	Previewable bool                  `json:"previewable"`
	Status      WorkspaceChangeStatus `json:"status,omitempty"`
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

type artifactFormat struct {
	MIME        string
	Kind        string
	Previewable bool
}

var artifactFormats = map[string]artifactFormat{
	".png":      {"image/png", "image", true},
	".jpg":      {"image/jpeg", "image", true},
	".jpeg":     {"image/jpeg", "image", true},
	".gif":      {"image/gif", "image", true},
	".webp":     {"image/webp", "image", true},
	".svg":      {"image/svg+xml", "image", true},
	".bmp":      {"image/bmp", "image", true},
	".ico":      {"image/x-icon", "image", true},
	".pdf":      {"application/pdf", "pdf", true},
	".md":       {"text/markdown", "markdown", true},
	".markdown": {"text/markdown", "markdown", true},
	".txt":      {"text/plain", "text", true},
	".log":      {"text/plain", "text", true},
	".csv":      {"text/csv", "text", true},
	".json":     {"application/json", "text", true},
	".yaml":     {"text/yaml", "text", true},
	".yml":      {"text/yaml", "text", true},
	".xml":      {"application/xml", "text", true},
	".html":     {"text/html", "text", true},
	".htm":      {"text/html", "text", true},
	".go":       {"text/x-go", "text", true},
	".ts":       {"text/typescript", "text", true},
	".tsx":      {"text/typescript", "text", true},
	".js":       {"text/javascript", "text", true},
	".jsx":      {"text/javascript", "text", true},
	".py":       {"text/x-python", "text", true},
	".rs":       {"text/x-rust", "text", true},
	".java":     {"text/x-java", "text", true},
	".c":        {"text/x-c", "text", true},
	".h":        {"text/x-c", "text", true},
	".cpp":      {"text/x-c++", "text", true},
	".sh":       {"text/x-shellscript", "text", true},
	".sql":      {"text/x-sql", "text", true},
	".toml":     {"text/plain", "text", true},
	".ini":      {"text/plain", "text", true},
	".css":      {"text/css", "text", true},
	".wasm":     {"application/wasm", "binary", false},
}

func artifactFormatForExt(ext string) artifactFormat {
	if format, ok := artifactFormats[ext]; ok {
		return format
	}
	return artifactFormat{MIME: "application/octet-stream", Kind: "binary"}
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
	format := artifactFormatForExt(ext)
	if !format.Previewable {
		return nil, fmt.Errorf("不支持预览该文件类型（%s）", filepath.Base(abs))
	}
	art := &Artifact{
		Name: filepath.Base(abs),
		Path: filepath.ToSlash(relPath),
		MIME: format.MIME,
		Size: st.Size(),
	}
	switch kind := format.Kind; kind {
	case "image", "pdf":
		if st.Size() > maxBinaryArtifact {
			return nil, fmt.Errorf("文件超过预览上限（%d MB）", maxBinaryArtifact>>20)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		art.Kind = kind
		art.DataURL = "data:" + format.MIME + ";base64," + base64.StdEncoding.EncodeToString(data)
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
	return buildTaskSnapshot(a.S.Store, sessionID, ws)
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
		if !info.Mode().IsRegular() {
			return nil
		}
		discovered++
		if len(files) >= maxSnapshotFiles {
			truncated = true
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		format := artifactFormatForExt(ext)
		files = append(files, WorkspaceFileInfo{
			Name:        d.Name(),
			Path:        relSlash,
			Kind:        format.Kind,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Previewable: format.Previewable,
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

// List returns previewable files under the workspace (newest first, capped).
func (a *ArtifactService) List() ([]ArtifactInfo, error) {
	ws := a.workspace()
	if ws == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	files, _, _, err := scanTaskFiles(ws)
	if err != nil {
		return nil, err
	}
	out := make([]ArtifactInfo, 0, len(files))
	for _, f := range files {
		if !f.Previewable {
			continue
		}
		out = append(out, ArtifactInfo{Name: f.Name, Path: f.Path, Kind: f.Kind, Size: f.Size})
	}
	if len(out) > snapshotListLimit {
		out = out[:snapshotListLimit]
	}
	return out, nil
}

// Workspace returns the configured workspace directory (for UI display).
func (a *ArtifactService) Workspace() string { return a.workspace() }
