package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

func TestRevealCommandPerPlatform(t *testing.T) {
	if cmd := revealCommand("darwin", "/tmp/a b/x.txt"); len(cmd.Args) != 3 || cmd.Args[1] != "-R" || cmd.Args[2] != "/tmp/a b/x.txt" {
		t.Fatalf("darwin reveal = %v", cmd.Args)
	}
	if cmd := revealCommand("windows", `C:\w\x.txt`); len(cmd.Args) != 2 || !strings.HasPrefix(cmd.Args[1], "/select,") {
		t.Fatalf("windows reveal = %v", cmd.Args)
	}
	if cmd := revealCommand("linux", "/w/x.txt"); len(cmd.Args) != 2 || cmd.Args[1] != "/w" {
		t.Fatalf("linux reveal = %v (must open the containing dir)", cmd.Args)
	}
}

func TestRevealWorkspaceFileValidation(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	app := &AppService{S: svc.S}
	dir := filepath.Join(ws, "tasks", "sess_reveal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = []config.ChatSession{{ID: "sess_reveal", WorkspaceDir: dir}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.RevealWorkspaceFile("sess_none", "a.bin"); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if err := app.RevealWorkspaceFile("sess_reveal", "../escape.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := app.RevealWorkspaceFile("sess_reveal", "missing.txt"); err == nil {
		t.Fatal("expected missing-file error")
	}
	// 合法路径：不再继续验证（会真的打开 Finder），只要求通过校验段——通过
	// 将 revealCommand 替换为 no-op 的方式。这里直接调用会启动 open，跳过。
}
