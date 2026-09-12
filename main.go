package main

import (
	"embed"
	"log"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"github.com/wailsapp/wails/v3/pkg/application"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
	"changeme/internal/services"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

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
	store, err := config.Open(cfgDir)
	if err != nil {
		log.Fatal(err)
	}

	// MCP manager + agent kit.
	mgr := mcpmgr.New()
	kit := agentkit.NewKit(store, mgr)
	svc := &services.Services{Store: store, Kit: kit, MCP: mgr}

	// Persistent ADK session storage (SQLite via pure-Go driver). The same
	// *gorm.DB is shared with ChatService so Regenerate can trim events.
	db, sessions, err := newSessionService(filepath.Join(cfgDir, "sessions.db"))
	if err != nil {
		log.Fatal(err)
	}

	app := application.New(application.Options{
		Name:        "WorkBuddy Agent",
		Description: "ADK-powered agent desktop app",
		Services: []application.Service{
			application.NewService(services.NewAppService(svc)),
			application.NewService(services.NewConfigService(svc)),
			application.NewService(services.NewMCPService(svc)),
			application.NewService(services.NewArtifactService(svc)),
			application.NewService(services.NewChatService(svc, sessions, db)),
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

// newSessionService opens the ADK session service backed by SQLite and
// returns the shared gorm handle alongside it.
func newSessionService(path string) (*gorm.DB, session.Service, error) {
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
	return db, svc, nil
}
