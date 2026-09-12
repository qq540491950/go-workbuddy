package main

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/wailsapp/wails/v3/pkg/application"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/loadmemorytool"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/memory"
	"changeme/internal/mcpmgr"
	"changeme/internal/services"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

// Version is the single source of truth for the app version, exposed to the
// frontend via AppService.AppInfo.
const Version = "0.3.0"

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

	// MCP manager + agent kit.
	mgr := mcpmgr.New()
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

	app := application.New(application.Options{
		Name:        "WorkBuddy Agent",
		Description: "ADK-powered agent desktop app",
		Services: []application.Service{
			application.NewService(services.NewAppService(svc)),
			application.NewService(services.NewConfigService(svc)),
			application.NewService(services.NewMCPService(svc)),
			application.NewService(services.NewArtifactService(svc)),
			application.NewService(services.NewChatService(svc, sessions, gormDB)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "WorkBuddy Agent",
		Width:  1280,
		Height: 820,
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

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupLogging writes the standard logger to a size-capped rotating file in
// addition to stderr. app.log moves to app.log.old once it exceeds 5 MB.
func setupLogging(logDir string) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "app.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(logPath, filepath.Join(logDir, "app.log.old"))
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags | log.LUTC)
}

// newSessionService opens the ADK session service backed by SQLite and
// returns the shared gorm handle alongside it. A corrupted database is moved
// aside (backed up) and recreated so the app always starts.
func newSessionService(path string) (*gorm.DB, session.Service, error) {
	open := func() (*gorm.DB, session.Service, error) {
		db, err := gorm.Open(sqlite.Open(path))
		if err != nil {
			return nil, nil, err
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
			return nil, nil, err
		}
		return db, svc, nil
	}
	db, svc, err := open()
	if err != nil {
		backup := path + ".corrupt-" + time.Now().Format("20060102-150405")
		_ = os.Rename(path, backup)
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
