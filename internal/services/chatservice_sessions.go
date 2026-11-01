package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/v2/session"

	"go-workbuddy/internal/config"
)

// NewSession creates a task session with an automatically isolated workspace.
func (c *ChatService) NewSession(targetType, targetID, title string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, "", "")
}

// NewSessionWithWorkspace creates a task session. An empty workspaceDir asks
// the backend to create a unique directory under the global workspace.
func (c *ChatService) NewSessionWithWorkspace(targetType, targetID, title, workspaceDir string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, workspaceDir, "")
}

// NewSessionWithProject creates a chat bound to a registered project. Project
// chats share the project root as their workspace and own no task directory,
// so many sessions can work in one project side by side.
func (c *ChatService) NewSessionWithProject(targetType, targetID, title, projectID string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, "", projectID)
}

func (c *ChatService) newSession(targetType, targetID, title, workspaceDir, projectID string) (config.ChatSession, error) {
	// An empty target is a direct chat: the turn runs on the default
	// provider's model with no persisted AgentConfig until the user
	// @-mentions one.
	if targetType != "" && targetType != "agent" && targetType != "team" && targetType != "remote" {
		return config.ChatSession{}, fmt.Errorf("invalid target type %q", targetType)
	}
	if title == "" {
		title = "新对话"
	}
	id := newID("sess_")
	createdDir := ""
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	projectName := ""
	if strings.TrimSpace(projectID) != "" {
		for _, p := range c.S.Store.Get().Projects {
			if p.ID == projectID {
				projectName = p.Name
				workspaceDir = p.RootDir
				break
			}
		}
		if projectName == "" {
			return config.ChatSession{}, fmt.Errorf("项目不存在或已被删除")
		}
	} else if strings.TrimSpace(workspaceDir) == "" {
		global := strings.TrimSpace(c.S.Store.Get().Settings.WorkspaceDir)
		if global == "" {
			return config.ChatSession{}, fmt.Errorf("工作区目录未配置")
		}
		workspaceDir = filepath.Join(global, "tasks", id)
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return config.ChatSession{}, fmt.Errorf("创建任务工作区失败: %w", err)
		}
		createdDir = workspaceDir
	}
	var err error
	workspaceDir, err = validateTaskWorkspace(workspaceDir)
	if err != nil {
		cleanupCreatedDir(createdDir)
		return config.ChatSession{}, err
	}
	if projectID == "" {
		if err := projectRootConflict(c.S.Store.Get(), workspaceDir); err != nil {
			cleanupCreatedDir(createdDir)
			return config.ChatSession{}, err
		}
	}
	if err := workspaceConflict(c.S.Store.Get(), workspaceDir, ""); err != nil {
		cleanupCreatedDir(createdDir)
		return config.ChatSession{}, err
	}
	if _, err := ensureTaskBaseline(c.S.Store, id, workspaceDir); err != nil {
		cleanupCreatedDir(createdDir)
		removeTaskBaseline(c.S.Store, id)
		return config.ChatSession{}, err
	}
	// Project chats keep WorkspaceDir empty: their workspace resolves through
	// ProjectID (see EffectiveWorkspaceFor), so re-rooting a project
	// propagates to every chat without rewriting session rows.
	storedWorkspace := workspaceDir
	if projectID != "" {
		storedWorkspace = ""
	}
	cs := config.ChatSession{
		ID:           id,
		Title:        title,
		TargetType:   targetType,
		TargetID:     targetID,
		CreatedAt:    nowUTC(),
		UpdatedAt:    nowUTC(),
		WorkspaceDir: storedWorkspace,
		ProjectID:    projectID,
	}
	cleanup := func() {
		cleanupCreatedDir(createdDir)
		removeTaskBaseline(c.S.Store, id)
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: cs.ID,
	}); err != nil {
		cleanup()
		return config.ChatSession{}, err
	}
	err = c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{cs}, cfg.Sessions...)
	})
	if err == nil {
		auditType := targetType
		if auditType == "" {
			auditType = "direct"
		}
		c.audit(cs.ID, "session_created", "system", auditType, mustJSON(map[string]string{"targetId": targetID, "title": cs.Title, "projectId": projectID}), "")
	} else {
		_ = c.sessions.Delete(context.Background(), &session.DeleteRequest{
			AppName: c.appName, UserID: c.userID, SessionID: cs.ID,
		})
		cleanup()
	}
	return cs, err
}

// ListProjects returns the registered projects in creation order.
func (c *ChatService) ListProjects() []config.Project {
	return c.S.Store.Get().Projects
}

// SaveProject creates a project (empty id) or updates name/root of an
// existing one (blank fields keep the stored value). The root must exist and
// be writable and may not be claimed by another project.
func (c *ChatService) SaveProject(id, name, rootDir string) (config.Project, error) {
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	if strings.TrimSpace(id) == "" {
		root, err := validateTaskWorkspace(rootDir)
		if err != nil {
			return config.Project{}, err
		}
		if err := projectRootConflict(c.S.Store.Get(), root); err != nil {
			return config.Project{}, err
		}
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(root)
		}
		p := config.Project{ID: newID("proj_"), Name: strings.TrimSpace(name), RootDir: root, CreatedAt: nowUTC()}
		if err := c.S.Store.Update(func(cfg *config.Config) {
			cfg.Projects = append(cfg.Projects, p)
		}); err != nil {
			return config.Project{}, err
		}
		c.audit("projects", "project_saved", "system", "project", mustJSON(map[string]string{"id": p.ID, "name": p.Name, "root": p.RootDir}), "")
		return p, nil
	}

	var updated config.Project
	var saveErr error
	err := c.S.Store.Update(func(cfg *config.Config) {
		idx := -1
		for i := range cfg.Projects {
			if cfg.Projects[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			saveErr = fmt.Errorf("项目 %q 不存在", id)
			return
		}
		p := &cfg.Projects[idx]
		root := p.RootDir
		if strings.TrimSpace(rootDir) != "" {
			canonical, err := validateTaskWorkspace(rootDir)
			if err != nil {
				saveErr = err
				return
			}
			for j := range cfg.Projects {
				if j != idx && samePath(cfg.Projects[j].RootDir, canonical) {
					saveErr = fmt.Errorf("目录已被项目「%s」使用", cfg.Projects[j].Name)
					return
				}
			}
			root = canonical
		}
		p.RootDir = root
		if strings.TrimSpace(name) != "" {
			p.Name = strings.TrimSpace(name)
		}
		updated = *p
	})
	if err != nil {
		return config.Project{}, err
	}
	if saveErr != nil {
		return config.Project{}, saveErr
	}
	c.audit("projects", "project_saved", "system", "project", mustJSON(map[string]string{"id": updated.ID, "name": updated.Name, "root": updated.RootDir}), "")
	return updated, nil
}

// DeleteProject unregisters a project. Its chats fall back to the default
// workspace and their baselines reset to the new directory; task files are
// never touched. Projects with running chats are refused.
func (c *ChatService) DeleteProject(id string) error {
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	cfg := c.S.Store.Get()
	found := false
	for _, p := range cfg.Projects {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("项目 %q 不存在", id)
	}
	for _, s := range cfg.Sessions {
		if s.ProjectID == id && c.sessionRunning(s.ID) {
			return fmt.Errorf("会话「%s」正在运行，不能删除项目", s.Title)
		}
	}
	if err := c.S.Store.Update(func(cfg *config.Config) {
		projects := make([]config.Project, 0, len(cfg.Projects))
		for _, p := range cfg.Projects {
			if p.ID != id {
				projects = append(projects, p)
			}
		}
		cfg.Projects = projects
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ProjectID == id {
				cfg.Sessions[i].ProjectID = ""
			}
		}
	}); err != nil {
		return err
	}
	// Baselines restart from the session's new effective (global) workspace
	// so the task panel does not report the project's files as mass deletions.
	for _, s := range cfg.Sessions {
		if s.ProjectID != id {
			continue
		}
		s.ProjectID = ""
		_, _ = resetTaskBaseline(c.S.Store, s.ID, s.EffectiveWorkspaceFor(c.S.Store.Get()))
	}
	c.audit("projects", "project_deleted", "system", "project", mustJSON(map[string]string{"id": id}), "")
	return nil
}

// SetSessionProject moves a chat into (empty id = out of) a project before
// its first turn. Joining hands the workspace to the project root: the task
// dir binding is cleared and the baseline restarts from the project.
func (c *ChatService) SetSessionProject(sessionID, projectID string) error {
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()

	if c.sessionRunning(sessionID) {
		return fmt.Errorf("会话正在运行，不能更改项目")
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
		return fmt.Errorf("首轮已开始，项目不可更改")
	}
	if strings.TrimSpace(projectID) != "" {
		known := false
		for _, p := range cfg.Projects {
			if p.ID == projectID {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("项目不存在或已被删除")
		}
	}
	var notFound error
	var revert func(*config.Config)
	err = c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID != sessionID {
				continue
			}
			oldProject, oldWorkspace := cfg.Sessions[i].ProjectID, cfg.Sessions[i].WorkspaceDir
			cfg.Sessions[i].ProjectID = projectID
			cfg.Sessions[i].WorkspaceDir = ""
			cfg.Sessions[i].UpdatedAt = nowUTC()
			revert = func(cfg *config.Config) {
				for i := range cfg.Sessions {
					if cfg.Sessions[i].ID == sessionID {
						cfg.Sessions[i].ProjectID = oldProject
						cfg.Sessions[i].WorkspaceDir = oldWorkspace
						return
					}
				}
			}
			return
		}
		notFound = fmt.Errorf("会话 %q 不存在", sessionID)
	})
	if err != nil {
		return err
	}
	if notFound != nil {
		return notFound
	}
	ws := ""
	for _, s := range c.S.Store.Get().Sessions {
		if s.ID == sessionID {
			ws = s.EffectiveWorkspaceFor(c.S.Store.Get())
		}
	}
	if _, err := resetTaskBaseline(c.S.Store, sessionID, ws); err != nil {
		_ = c.S.Store.Update(revert)
		return err
	}
	c.audit(sessionID, "session_project_set", "system", "session", mustJSON(map[string]string{"projectId": projectID}), "")
	return nil
}

func (c *ChatService) sessionRunning(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active[sessionID]) > 0
}

// ListSessions returns all chat sessions, pinned first, then newest first.
func (c *ChatService) ListSessions() []config.ChatSession {
	sessions := c.S.Store.Get().Sessions
	sorted := make([]config.ChatSession, len(sessions))
	copy(sorted, sessions)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Pinned != sorted[j].Pinned {
			return sorted[i].Pinned
		}
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})
	return sorted
}

// SearchSessions filters sessions by title OR by message content (a LIKE
// probe over the stored event payloads). Empty query = all sessions
// (pinned first, then most recently active).
func (c *ChatService) SearchSessions(query string) []config.ChatSession {
	sessions := c.ListSessions()
	q := strings.TrimSpace(query)
	if q == "" {
		return sessions
	}
	matched := map[string]bool{}
	for i := range sessions {
		if strings.Contains(sessions[i].Title, q) {
			matched[sessions[i].ID] = true
		}
	}
	if c.db != nil {
		var ids []string
		// Escape LIKE wildcards so searching "50%" matches the literal text,
		// not every content containing "50".
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		pattern := "%" + escaped + "%"
		if err := c.db.Raw("SELECT DISTINCT session_id FROM events WHERE content LIKE ? ESCAPE '\\' AND app_name = ? AND user_id = ?",
			pattern, c.appName, c.userID).Pluck("session_id", &ids); err == nil {
			for _, id := range ids {
				matched[id] = true
			}
		}
	}
	out := make([]config.ChatSession, 0, len(matched))
	for _, s := range sessions {
		if matched[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// SetSessionModel pins a chat to a specific model of its target agent's
// provider (per-session model override); empty clears the override.
func (c *ChatService) SetSessionModel(sessionID, modelID string) error {
	err := c.mutateSession(sessionID, func(s *config.ChatSession) { s.ModelOverride = strings.TrimSpace(modelID) })
	if err == nil {
		c.audit(sessionID, "session_model_set", "system", "session", mustJSON(map[string]string{"model": modelID}), "")
	}
	return err
}

// ArchiveSession toggles the archived flag of a session (archived sessions
// are hidden from the default sidebar list but never deleted).
func (c *ChatService) ArchiveSession(id string, archived bool) error {
	if !archived {
		c.workspaceMu.Lock()
		defer c.workspaceMu.Unlock()
	}
	err := c.mutateSessionGuarded(id, func(s *config.ChatSession, cfg *config.Config) error {
		if !archived {
			if ws := strings.TrimSpace(s.WorkspaceDir); ws != "" {
				if err := workspaceConflictForUnarchive(*cfg, *s); err != nil {
					return err
				}
			}
			s.UpdatedAt = nowUTC()
		}
		s.Archived = archived
		return nil
	})
	if err == nil {
		phase := "session_archived"
		if !archived {
			phase = "session_unarchived"
		}
		c.audit(id, phase, "system", "session", mustJSON(map[string]bool{"archived": archived}), "")
	}
	return err
}

// PinSession toggles the pinned flag of a session.
func (c *ChatService) PinSession(id string, pinned bool) error {
	return c.mutateSession(id, func(s *config.ChatSession) { s.Pinned = pinned })
}

// mutateSession applies fn to one stored session and reports unknown IDs as
// errors — the shared found/notFound shape behind rename, model override,
// pin and archive.
func (c *ChatService) mutateSession(id string, fn func(*config.ChatSession)) error {
	return c.mutateSessionGuarded(id, func(s *config.ChatSession, _ *config.Config) error {
		fn(s)
		return nil
	})
}

// mutateSessionGuarded is mutateSession with a validation hook that can abort
// the mutation (used by unarchive's workspace ownership check).
func (c *ChatService) mutateSessionGuarded(id string, fn func(*config.ChatSession, *config.Config) error) error {
	var notFound error
	var guardErr error
	err := c.S.Store.Update(func(cfg *config.Config) {
		found := false
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == id {
				found = true
				if guardErr = fn(&cfg.Sessions[i], cfg); guardErr != nil {
					return
				}
			}
		}
		if !found {
			notFound = fmt.Errorf("会话 %q 不存在", id)
		}
	})
	if err == nil {
		err = notFound
	}
	if err == nil {
		err = guardErr
	}
	return err
}

// RenameSession updates a session title. Unknown IDs are an error, and the
// audit record is only written on success so a failed rename does not leave a
// false "renamed" entry behind.
func (c *ChatService) RenameSession(id, title string) error {
	err := c.mutateSession(id, func(s *config.ChatSession) { s.Title = title })
	if err == nil {
		c.audit(id, "session_renamed", "system", "session", mustJSON(map[string]string{"title": title}), "")
	}
	return err
}

// DeleteSession removes a chat and its stored events. The config entry is
// dropped only after the database delete succeeds; otherwise the session
// would vanish from the sidebar while its rows stayed behind (and could be
// resurrected by fork/search). An in-flight run is cancelled first: an
// orphaned turn would keep burning tokens and re-create events for the
// deleted session until its timeout.
func (c *ChatService) DeleteSession(id string) error {
	return c.deleteSessionInternal(id)
}

// DeleteArchivedSessions deletes every archived session (cancel-first, events
// included) and returns how many were removed.
func (c *ChatService) DeleteArchivedSessions() (int, error) {
	var ids []string
	for _, s := range c.S.Store.Get().Sessions {
		if s.Archived {
			ids = append(ids, s.ID)
		}
	}
	deleted := 0
	for _, id := range ids {
		if err := c.deleteSessionInternal(id); err != nil {
			return deleted, fmt.Errorf("删除会话 %q 失败: %w", id, err)
		}
		deleted++
	}
	return deleted, nil
}

func (c *ChatService) deleteSessionInternal(id string) error {
	c.cancelAndWaitBounded(id)
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	if err := c.sessions.Delete(context.Background(), &session.DeleteRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		return fmt.Errorf("delete session events: %w", err)
	}
	// The ADK session service deletes only the sessions row — event rows are
	// left behind as orphans that full-text session search keeps matching
	// forever. Remove them explicitly once the in-flight run has unwound (so
	// nothing can write new rows after this).
	if c.db != nil {
		if err := c.db.WithContext(context.Background()).Exec(
			"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
			c.appName, c.userID, id,
		).Error; err != nil {
			return fmt.Errorf("delete session event rows: %w", err)
		}
	}
	var notFound error
	if err := c.S.Store.Update(func(cfg *config.Config) {
		found := false
		out := cfg.Sessions[:0]
		for _, s := range cfg.Sessions {
			if s.ID != id {
				out = append(out, s)
			} else {
				found = true
			}
		}
		cfg.Sessions = out
		if !found {
			notFound = fmt.Errorf("会话 %q 不存在", id)
		}
	}); err != nil {
		return err
	}
	if notFound != nil {
		return notFound
	}
	removeTaskBaseline(c.S.Store, id)
	delete(c.workspaceLocks, id)
	c.audit(id, "session_deleted", "system", "session", mustJSON(map[string]string{"sessionId": id}), "")
	return nil
}

// GetSessionMessages renders the stored ADK events for a session.
func (c *ChatService) GetSessionMessages(sessionID string) ([]ChatMessage, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Session == nil {
		return nil, nil
	}
	var out []ChatMessage
	i := 0
	cfg := c.S.Store.Get()
	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		i++
		msgs := renderEvent(cfg, ev, i)
		out = append(out, msgs...)
	}
	return out, nil
}

// ForkSession duplicates a session's full ADK event history into a new chat
// (branching): the user can regenerate/edit/confirm differently in the fork
// without touching the original timeline.
func (c *ChatService) ForkSession(sessionID string) (config.ChatSession, error) {
	if c.db == nil {
		return config.ChatSession{}, fmt.Errorf("会话数据库不可用")
	}
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	cfg := c.S.Store.Get()
	src := c.resolveSessionTarget(cfg, sessionID)
	if src == nil {
		return config.ChatSession{}, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	id := newID("sess_")
	workspace := src.EffectiveWorkspaceFor(cfg)
	if _, err := resetTaskBaseline(c.S.Store, id, workspace); err != nil {
		return config.ChatSession{}, err
	}
	cleanupBranch := func() {
		removeTaskBaseline(c.S.Store, id)
		_ = c.sessions.Delete(context.Background(), &session.DeleteRequest{
			AppName: c.appName, UserID: c.userID, SessionID: id,
		})
		if c.db != nil {
			_ = c.db.WithContext(context.Background()).Exec(
				"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
				c.appName, c.userID, id,
			).Error
		}
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		removeTaskBaseline(c.S.Store, id)
		return config.ChatSession{}, err
	}

	// Copy every stored event row to the new session. The events table key
	// includes session_id, so ids may repeat across sessions. The column list
	// comes from the schema itself to survive ADK schema updates.
	var cols []string
	if err := c.db.Raw("SELECT name FROM pragma_table_info('events')").Pluck("name", &cols).Error; err != nil {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("读取事件表结构失败: %w", err)
	}
	if len(cols) == 0 {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("事件表不存在")
	}
	selects := make([]string, len(cols))
	for i, col := range cols {
		if col == "session_id" {
			selects[i] = "? AS session_id"
		} else {
			selects[i] = col
		}
	}
	if err := c.db.Exec(
		fmt.Sprintf("INSERT INTO events (%s) SELECT %s FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
			strings.Join(cols, ","), strings.Join(selects, ",")),
		id, c.appName, c.userID, sessionID,
	).Error; err != nil {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("复制会话事件失败: %w", err)
	}

	branch := config.ChatSession{
		ID:           id,
		Title:        strings.TrimSpace(src.Title + "（分支）"),
		TargetType:   src.TargetType,
		TargetID:     src.TargetID,
		CreatedAt:    nowUTC(),
		UpdatedAt:    nowUTC(),
		ForkedFrom:   sessionID,
		WorkspaceDir: src.WorkspaceDir,
		ProjectID:    src.ProjectID,
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{branch}, cfg.Sessions...)
	})
	if err == nil {
		c.audit(branch.ID, "session_forked", "system", "session", mustJSON(map[string]string{"forkedFrom": sessionID}), "")
	} else {
		cleanupBranch()
	}
	return branch, err
}
