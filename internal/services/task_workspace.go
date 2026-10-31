package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"google.golang.org/adk/v2/session"

	"changeme/internal/config"
)

// SetSessionWorkspace rebinds a task before its first event. The directory is
// canonicalized and checked for writability and active-session conflicts.
func (c *ChatService) SetSessionWorkspace(sessionID, workspaceDir string) error {
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()

	if c.sessionRunning(sessionID) {
		return fmt.Errorf("会话正在运行，不能更改工作区")
	}
	cfg := c.S.Store.Get()
	found := false
	for _, s := range cfg.Sessions {
		if s.ID == sessionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	started, err := c.sessionHasEvents(sessionID)
	if err != nil {
		return err
	}
	if started {
		return fmt.Errorf("首轮已开始，任务工作区不可更改")
	}
	ws, err := validateTaskWorkspace(workspaceDir)
	if err != nil {
		return err
	}
	if err := workspaceConflict(cfg, ws, sessionID); err != nil {
		return err
	}
	var notFound error
	oldWorkspace := ""
	err = c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == sessionID {
				oldWorkspace = cfg.Sessions[i].WorkspaceDir
				cfg.Sessions[i].WorkspaceDir = ws
				cfg.Sessions[i].UpdatedAt = nowUTC()
				return
			}
		}
		notFound = fmt.Errorf("会话 %q 不存在", sessionID)
	})
	if err != nil {
		return err
	}
	if notFound != nil {
		return notFound
	}
	if _, err := resetTaskBaseline(c.S.Store, sessionID, ws); err != nil {
		_ = c.S.Store.Update(func(cfg *config.Config) {
			for i := range cfg.Sessions {
				if cfg.Sessions[i].ID == sessionID {
					cfg.Sessions[i].WorkspaceDir = oldWorkspace
					return
				}
			}
		})
		return err
	}
	c.audit(sessionID, "session_workspace_set", "system", "session", mustJSON(map[string]string{"workspace": ws}), "")
	return nil
}

func (c *ChatService) sessionWorkspaceLock(sessionID string) *sync.Mutex {
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	lock := c.workspaceLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		c.workspaceLocks[sessionID] = lock
	}
	return lock
}

// SessionWorkspace returns the effective task directory after checking that
// it still exists and is readable. The UI uses this instead of reproducing the
// legacy fallback rule.
func (c *ChatService) SessionWorkspace(sessionID string) (string, error) {
	cfg := c.S.Store.Get()
	for _, chatSession := range cfg.Sessions {
		if chatSession.ID != sessionID {
			continue
		}
		ws := chatSession.EffectiveWorkspace(cfg.Settings.WorkspaceDir)
		if strings.TrimSpace(ws) == "" {
			return "", fmt.Errorf("任务工作区未配置")
		}
		st, err := os.Stat(ws)
		if err != nil {
			return "", fmt.Errorf("任务工作区不可用: %w", err)
		}
		if !st.IsDir() {
			return "", fmt.Errorf("任务工作区不是目录")
		}
		if _, err := os.ReadDir(ws); err != nil {
			return "", fmt.Errorf("任务工作区不可读: %w", err)
		}
		return ws, nil
	}
	return "", fmt.Errorf("会话 %q 不存在", sessionID)
}

func (c *ChatService) sessionHasEvents(sessionID string) (bool, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return false, err
	}
	if resp.Session == nil {
		return false, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	for ev := range resp.Session.Events().All() {
		if ev != nil {
			return true, nil
		}
	}
	return false, nil
}

// validateTaskWorkspace canonicalizes a user-selected task directory and
// verifies that it can be listed and written.
func validateTaskWorkspace(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("请选择任务工作目录")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("任务工作目录必须使用绝对路径")
	}
	abs, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("工作区不可用: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("工作区不可用: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("工作区必须是目录")
	}
	if _, err := os.ReadDir(abs); err != nil {
		return "", fmt.Errorf("工作区不可读: %w", err)
	}
	probe, err := os.CreateTemp(abs, ".workbuddy-write-test-*")
	if err != nil {
		return "", fmt.Errorf("工作区不可写: %w", err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	return abs, nil
}

func workspaceConflict(cfg config.Config, workspace, excludeSessionID string) error {
	for _, s := range cfg.Sessions {
		if s.ID == excludeSessionID || s.Archived || strings.TrimSpace(s.WorkspaceDir) == "" {
			continue
		}
		if samePath(s.WorkspaceDir, workspace) {
			return fmt.Errorf("工作区已被任务「%s」使用，请选择其他目录", s.Title)
		}
	}
	return nil
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func cleanupCreatedDir(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
