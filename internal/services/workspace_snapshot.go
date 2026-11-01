package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go-workbuddy/internal/config"
)

// taskSnapshotVersion v2 adds per-file baseline content snapshots (see
// taskBaselineFile.Content). A stored v1 baseline is treated as stale and
// rebuilt so line-level diff works immediately after upgrade.
const taskSnapshotVersion = 2

type taskBaselineFile struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	// Content is the file name (inside the session's content dir) of the
	// baseline content snapshot, empty when the file was not snapshotable
	// (binary, empty, or over the per-file/budget caps).
	Content string `json:"content,omitempty"`
}

type taskBaseline struct {
	Version    int                         `json:"version"`
	SessionID  string                      `json:"sessionId"`
	Workspace  string                      `json:"workspace"`
	BaselineAt time.Time                   `json:"baselineAt"`
	Files      map[string]taskBaselineFile `json:"files"`
}

var taskSnapshotMu sync.Mutex

func taskSnapshotDir(store *config.Store) string {
	return filepath.Join(store.Dir(), "task-snapshots")
}

func taskSnapshotPath(store *config.Store, sessionID string) string {
	return filepath.Join(taskSnapshotDir(store), filepath.Base(sessionID)+".json")
}

func ensureTaskBaseline(store *config.Store, sessionID, workspace string) (*taskBaseline, error) {
	taskSnapshotMu.Lock()
	defer taskSnapshotMu.Unlock()
	baseline, _, err := loadTaskBaselineLocked(store, sessionID, workspace)
	return baseline, err
}

func resetTaskBaseline(store *config.Store, sessionID, workspace string) (*taskBaseline, error) {
	taskSnapshotMu.Lock()
	defer taskSnapshotMu.Unlock()
	baseline, err := createTaskBaselineLocked(store, sessionID, workspace)
	return baseline, err
}

func removeTaskBaseline(store *config.Store, sessionID string) {
	taskSnapshotMu.Lock()
	defer taskSnapshotMu.Unlock()
	_ = os.Remove(taskSnapshotPath(store, sessionID))
	removeTaskContent(store, sessionID)
}

func loadTaskBaselineLocked(store *config.Store, sessionID, workspace string) (*taskBaseline, bool, error) {
	data, err := os.ReadFile(taskSnapshotPath(store, sessionID))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, false, fmt.Errorf("读取任务快照失败: %w", err)
		}
		baseline, err := createTaskBaselineLocked(store, sessionID, workspace)
		return baseline, true, err
	}
	var baseline taskBaseline
	if err := json.Unmarshal(data, &baseline); err != nil ||
		baseline.Version != taskSnapshotVersion ||
		baseline.SessionID != sessionID ||
		!samePath(baseline.Workspace, workspace) {
		// A corrupt or stale baseline is recoverable: preserve it as .bak and
		// start a fresh task baseline rather than making the result pane unusable.
		_ = os.Rename(taskSnapshotPath(store, sessionID), taskSnapshotPath(store, sessionID)+".bak")
		baseline, err := createTaskBaselineLocked(store, sessionID, workspace)
		return baseline, true, err
	}
	if baseline.Files == nil {
		baseline.Files = map[string]taskBaselineFile{}
	}
	return &baseline, false, nil
}

func createTaskBaselineLocked(store *config.Store, sessionID, workspace string) (*taskBaseline, error) {
	// A fresh baseline invalidates any content snapshots left by a previous
	// (stale, corrupt or reset) baseline for this session.
	removeTaskContent(store, sessionID)
	files, _, _, err := scanTaskFiles(workspace)
	if err != nil {
		return nil, fmt.Errorf("建立任务快照基线失败: %w", err)
	}
	baseline := &taskBaseline{
		Version:    taskSnapshotVersion,
		SessionID:  sessionID,
		Workspace:  workspace,
		BaselineAt: time.Now().UTC(),
		Files:      make(map[string]taskBaselineFile, len(files)),
	}
	for _, f := range files {
		baseline.Files[f.Path] = taskBaselineFile{Size: f.Size, ModTime: f.ModTime}
	}
	snapshotBaselineContent(store, sessionID, workspace, files, baseline)
	if err := writeTaskBaselineLocked(store, baseline); err != nil {
		return nil, err
	}
	return baseline, nil
}

// snapshotBaselineContent stores the text of small files so later diffs can
// compare against the task baseline. Best-effort: read or write failures only
// skip the file (its diff will report missing baseline content).
func snapshotBaselineContent(store *config.Store, sessionID, workspace string, files []WorkspaceFileInfo, baseline *taskBaseline) {
	budget := int64(diffContentBudget)
	stored := 0
	for _, f := range files {
		if stored >= diffContentFileCap || budget < f.Size {
			break
		}
		if f.Size == 0 || f.Size > diffContentFileMax {
			continue
		}
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(f.Path)))
		if err != nil || !looksTextual(data) {
			continue
		}
		name, err := writeTaskContent(store, sessionID, f.Path, data)
		if err != nil {
			continue
		}
		entry := baseline.Files[f.Path]
		entry.Content = name
		baseline.Files[f.Path] = entry
		budget -= int64(len(data))
		stored++
	}
}

func writeTaskBaselineLocked(store *config.Store, baseline *taskBaseline) error {
	dir := taskSnapshotDir(store)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建任务快照目录失败: %w", err)
	}
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("编码任务快照失败: %w", err)
	}
	path := taskSnapshotPath(store, baseline.SessionID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入任务快照失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("提交任务快照失败: %w", err)
	}
	return nil
}

func buildTaskSnapshot(store *config.Store, sessionID, workspace string) (*TaskWorkspaceSnapshot, error) {
	taskSnapshotMu.Lock()
	defer taskSnapshotMu.Unlock()
	baseline, _, err := loadTaskBaselineLocked(store, sessionID, workspace)
	if err != nil {
		return nil, err
	}
	files, discovered, scanTruncated, err := scanTaskFiles(workspace)
	if err != nil {
		return nil, err
	}
	current := make(map[string]WorkspaceFileInfo, len(files))
	var artifacts []WorkspaceFileInfo
	var changes []WorkspaceFileInfo
	added, modified := 0, 0
	for _, f := range files {
		current[f.Path] = f
		base, existed := baseline.Files[f.Path]
		status := WorkspaceChangeStatus("")
		switch {
		case !existed:
			status = ChangeAdded
			added++
		case base.Size != f.Size || !base.ModTime.Equal(f.ModTime):
			status = ChangeModified
			modified++
		}
		if status == "" {
			continue
		}
		change := f
		change.Status = status
		changes = append(changes, change)
		artifacts = append(artifacts, change)
	}
	deleted := 0
	for path, base := range baseline.Files {
		if _, ok := current[path]; ok {
			continue
		}
		deleted++
		ext := strings.ToLower(filepath.Ext(path))
		format := artifactFormatForExt(ext)
		changes = append(changes, WorkspaceFileInfo{
			Name:        filepath.Base(path),
			Path:        path,
			Kind:        format.Kind,
			Size:        base.Size,
			ModTime:     base.ModTime,
			Previewable: format.Previewable,
			Status:      ChangeDeleted,
		})
	}
	sortWorkspaceFiles(artifacts)
	sortWorkspaceFiles(changes)

	truncated := scanTruncated
	files, truncated = capWorkspaceFiles(files, truncated)
	artifacts, truncated = capWorkspaceFiles(artifacts, truncated)
	changes, truncated = capWorkspaceFiles(changes, truncated)
	baselineAt := baseline.BaselineAt
	return &TaskWorkspaceSnapshot{
		SessionID:   sessionID,
		Workspace:   workspace,
		Files:       files,
		Artifacts:   artifacts,
		Changes:     changes,
		Discovered:  discovered,
		Added:       added,
		Modified:    modified,
		Deleted:     deleted,
		Truncated:   truncated,
		BaselineAt:  &baselineAt,
		RefreshedAt: time.Now().UTC(),
	}, nil
}

func sortWorkspaceFiles(files []WorkspaceFileInfo) {
	sort.Slice(files, func(i, j int) bool {
		if !files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ModTime.After(files[j].ModTime)
		}
		return files[i].Path < files[j].Path
	})
}

func capWorkspaceFiles(files []WorkspaceFileInfo, truncated bool) ([]WorkspaceFileInfo, bool) {
	if len(files) > snapshotListLimit {
		return files[:snapshotListLimit], true
	}
	return files, truncated
}
