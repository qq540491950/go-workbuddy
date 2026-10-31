package main

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/wailsapp/wails/v3/pkg/application"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"google.golang.org/adk/v2/tool/loadmemorytool"
	"gorm.io/gorm"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
	"changeme/internal/memory"
	"changeme/internal/services"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

// Version is the single source of truth for the app version, exposed to the
// frontend via AppService.AppInfo.
const Version = "0.4.0"

func init() {
	// Register the chat streaming event so the generated bindings carry a
	// strongly typed JS/TS API for it.
	application.RegisterEvent[services.ChatStreamEvent](services.EventChatStream)
}

func main() {
	// Configuration lives under the OS user config directory.
	cfgDir, err := osUserConfigDir("workbuddy-agent")
	if err != nil {
		log.Fatal(err)
	}
	setupLogging(filepath.Join(cfgDir, "logs"))

	store, err := config.Open(cfgDir)
	if err != nil {
		log.Fatal(err)
	}

	// Outbound proxy for model/embedding/MCP traffic. Applies to
	// http.DefaultTransport, so it must run before any HTTP request;
	// changing the setting requires an app restart.
	if err := agentkit.ApplyGlobalProxy(store.Get().Settings.HTTPProxy); err != nil {
		log.Printf("代理配置无效（使用直连）: %v", err)
	}

	// MCP manager + agent kit.
	mgr := mcpmgr.New()
	mgr.SetTokenStore(store) // OAuth tokens persist across restarts
	mgr.SetAuthPendingHook(func(server, authURL string) {
		if app := application.Get(); app != nil {
			app.Event.Emit("mcp:auth-pending", map[string]string{"server": server, "url": authURL})
		}
	})
	kit := agentkit.NewKit(store, mgr)
	svc := &services.Services{Store: store, Kit: kit, MCP: mgr}
	services.AppVersion = Version

	// Persistent ADK session storage (SQLite via pure-Go driver). The same
	// *gorm.DB is shared with ChatService (Regenerate) and long-term memory.
	gormDB, sessions, err := newSessionService(filepath.Join(cfgDir, "sessions.db"))
	if err != nil {
		log.Fatal(err)
	}

	// Long-term memory (SQLite, same DB as sessions).
	memSvc, err := memory.NewSQLiteService(gormDB)
	if err != nil {
		log.Fatal(err)
	}
	kit.SetMemoryTool(loadmemorytool.New())
	svc.Memory = memSvc

	// Knowledge vector index (semantic search upgrade; failure degrades to
	// keyword search, so it must not block startup).
	if ki, err := agentkit.OpenKnowledgeIndex(filepath.Join(cfgDir, "knowledge.index.db")); err == nil {
		kit.SetKnowledgeIndex(ki)
	} else {
		log.Printf("knowledge index disabled: %v", err)
	}

	// Always register the A2A service (start/stop RPCs); auto-start may have
	// begun serving already via the settings hook below.
	a2aRPC := services.NewA2AService(svc, sessions)
	svc.A2A = a2aRPC // config mutations can take published agents offline

	chatSvc := services.NewChatService(svc, sessions, gormDB)
	chatSvc.SetToolAuditPath(filepath.Join(cfgDir, "logs", "tools-audit.jsonl"))
	logSvc := services.NewAppService(svc)
	logSvc.SetLogPath(filepath.Join(cfgDir, "logs", "app.log"))
	logSvc.SetToolAuditPath(filepath.Join(cfgDir, "logs", "tools-audit.jsonl"))
	logSvc.SetStoragePaths(filepath.Join(cfgDir, "sessions.db"), filepath.Join(cfgDir, "knowledge.index.db"), gormDB)

	app := application.New(application.Options{
		Name:        "WorkBuddy Agent",
		Description: "ADK-powered agent desktop app",
		Services: []application.Service{
			application.NewService(logSvc),
			application.NewService(services.NewConfigService(svc)),
			application.NewService(services.NewMCPService(svc)),
			application.NewService(services.NewArtifactService(svc)),
			application.NewService(chatSvc),
			application.NewService(a2aRPC),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// The tray + global shortcut are resident features: closing the
			// window must not quit the whole app (it would silently kill the
			// published A2A servers and the summon shortcut). Quit lives in
			// the tray menu.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			mgr.CloseAll()   // stdio MCP 子进程不孤儿化
			a2aRPC.StopAll() // graceful: unpublish agents before exit
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "WorkBuddy Agent",
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 600,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(18, 18, 20),
		URL:              "/",
	})

	// A2A server auto-start (publish a local agent for remote A2A clients).
	if svcSettings := store.Get().Settings; svcSettings.A2AAutoStart && svcSettings.A2AAgentID != "" {
		if st, err := a2aRPC.Start(svcSettings.A2AAgentID, svcSettings.A2APort); err != nil {
			log.Printf("A2A 服务自启动失败: %v", err)
		} else {
			log.Printf("A2A 服务已自启动: %s", st.URL)
		}
	}

	setupSystemTray(app, window)
	// Global summon shortcut; a conflicting registration (another app owns
	// it) must not block startup.
	if err := app.GlobalShortcut.Register("shift+cmd+space", func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	}); err != nil {
		log.Printf("全局快捷键注册失败（不影响启动）: %v", err)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupSystemTray adds a menu bar tray: click toggles the window, the menu
// offers show/hide and quit (类 WorkBuddy 的常驻入口).
func setupSystemTray(app *application.App, window *application.WebviewWindow) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("WorkBuddy Agent")
	// 左键直接切换主窗口显隐（smart default），右键弹出菜单。
	tray.AttachWindow(window).WindowOffset(8)

	menu := app.NewMenu()
	menu.Add("显示/隐藏 WorkBuddy").OnClick(func(*application.Context) {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	})
	menu.Add("退出").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
}

// setupLogging writes the standard logger to a size-capped rotating file in
// addition to stderr. app.log moves to app.log.old once it exceeds 5 MB.
func setupLogging(logDir string) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	rw, err := newRotatingWriter(filepath.Join(logDir, "app.log"), 5<<20)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, rw))
	log.SetFlags(log.LstdFlags | log.LUTC)
}

// rotatingWriter caps its file at maxBytes: tray apps stay resident for
// weeks, and the startup-only rotation check used to let app.log grow
// without bound within one session. Every write checks the size and rotates
// to <path>.old before appending.
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
}

func newRotatingWriter(path string, maxBytes int64) (*rotatingWriter, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxBytes {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &rotatingWriter{path: path, maxBytes: maxBytes, f: f}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A failed reopen leaves w.f nil; every later write retries the open so a
	// transient EMFILE/permission error does not kill file logging for the
	// rest of the process lifetime.
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		w.f = f
	}
	st, err := w.f.Stat()
	if err == nil && st.Size()+int64(len(p)) > w.maxBytes {
		_ = w.f.Close()
		_ = os.Rename(w.path, w.path+".old")
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			w.f = nil // retried on the next write
			return 0, err
		}
		w.f = f
	}
	return w.f.Write(p)
}

// Close releases the current log file handle (used by tests so TempDir
// cleanup does not fail on Windows where open handles block deletion).
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// newSessionService opens the ADK session service backed by SQLite and
// returns the shared gorm handle alongside it. A corrupted database is moved
// aside (backed up) and recreated so the app always starts.
func newSessionService(path string) (*gorm.DB, session.Service, error) {
	open := func() (*gorm.DB, session.Service, error) {
		dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		db, err := gorm.Open(sqlite.Open(dsn))
		if err != nil {
			return nil, nil, err
		}
		// Serialize all access: parallel sessions write this DB concurrently,
		// and WAL alone cannot help a deferred transaction that hits a snapshot
		// conflict (SQLITE_BUSY bypasses busy_timeout). One connection queues
		// every statement instead — plenty for a desktop app.
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.SetMaxOpenConns(1)
		}
		svc, err := database.NewSessionServiceFromDB(db)
		if err != nil {
			return nil, nil, err
		}
		if err := database.AutoMigrate(svc); err != nil {
			return nil, nil, err
		}
		// Probe: a corrupt file can pass Open but fail on first query.
		if err := db.Exec("SELECT count(*) FROM sessions").Error; err != nil {
			closeDB(db) // release the pool before the corrupt-file rename
			return nil, nil, err
		}
		return db, svc, nil
	}
	db, svc, err := open()
	if err != nil {
		backup := path + ".corrupt-" + time.Now().Format("20060102-150405")
		// Close any leaked handle first: Windows refuses to rename a file
		// that is still open (sharing violation), which would turn the
		// self-heal path into a hard startup failure.
		closeDB(db)
		if rerr := os.Rename(path, backup); rerr != nil {
			log.Printf("sessions database corrupt but backup rename failed: %v", rerr)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Rename(path+suffix, backup+suffix)
		}
		log.Printf("sessions database was corrupt, moved to %s and recreated", backup)
		db, svc, err = open()
		if err != nil {
			return nil, nil, err
		}
	}
	return db, svc, nil
}

// closeDB releases the underlying SQL pool of a gorm handle (best effort).
func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
