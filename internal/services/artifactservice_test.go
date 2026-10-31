package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

func newArtifactFixture(t *testing.T) (*ArtifactService, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = ws
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewArtifactService(svc), ws
}

func TestOpenImageArtifact(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(ws, "chart.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	art, err := svc.Open("chart.png")
	if err != nil {
		t.Fatal(err)
	}
	if art.Kind != "image" || art.MIME != "image/png" {
		t.Fatalf("kind/mime = %s/%s", art.Kind, art.MIME)
	}
	if !strings.HasPrefix(art.DataURL, "data:image/png;base64,") {
		t.Fatalf("dataURL prefix missing: %q", art.DataURL[:40])
	}
	if len(art.DataURL) < len(png) {
		t.Fatal("payload too short")
	}
}

func TestOpenMarkdownArtifact(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	if err := os.WriteFile(filepath.Join(ws, "sub", "report.md"), []byte("# 标题\n内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	art, err := svc.Open("sub/report.md")
	if err != nil {
		t.Fatal(err)
	}
	if art.Kind != "markdown" || !strings.Contains(art.Text, "# 标题") || !strings.Contains(art.Text, "内容") {
		t.Fatalf("markdown artifact = %+v", art)
	}
	if art.DataURL != "" {
		t.Fatal("text artifacts must not carry dataURL")
	}
}

// TestOpenTruncatesLargeText pins the preview contract for oversized text
// files: the tail is cut at a rune boundary with the truncated flag set, and
// the payload stays bounded (the read itself stops at the cap).
func TestOpenTruncatesLargeText(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	big := strings.Repeat("汉", 200*1024) // 600KB of UTF-8, cap is 512KB
	if err := os.WriteFile(filepath.Join(ws, "big.log"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	art, err := svc.Open("big.log")
	if err != nil {
		t.Fatal(err)
	}
	if !art.Truncated {
		t.Fatal("expected truncated flag")
	}
	if len(art.Text) > 512*1024+20 { // cap + truncation marker
		t.Fatalf("text payload exceeds cap: %d bytes", len(art.Text))
	}
	if !utf8.ValidString(art.Text) {
		t.Fatal("truncated preview is not valid UTF-8")
	}
}

func TestOpenRejectsTraversalAndBinary(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	if _, err := svc.Open("../escape.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := os.WriteFile(filepath.Join(ws, "bin.dat"), []byte{0x00, 0x01, 0xFF, 0xFE}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open("bin.dat"); err == nil {
		t.Fatal("expected binary rejection")
	}
	_ = ws
}

func TestListArtifacts(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	// Explicit mtimes: List promises newest first.
	for i, name := range []string{"a.png", "b.md", "c.txt", "d.exe"} {
		p := filepath.Join(ws, name)
		_ = os.WriteFile(p, []byte{byte(i)}, 0o644)
		stamp := time.Now().Add(-time.Duration(3-i) * time.Hour) // a oldest, c newest
		_ = os.Chtimes(p, stamp, stamp)
	}
	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	// d.exe has an unknown extension and must be excluded; newest first.
	if strings.Join(names, ",") != "c.txt,b.md,a.png" {
		t.Fatalf("list = %v", names)
	}
}

func TestOpenSessionArtifactIsIsolated(t *testing.T) {
	svc, global := newArtifactFixture(t)
	dirA := filepath.Join(global, "tasks", "a")
	dirB := filepath.Join(global, "tasks", "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "same.md"), []byte("# A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "same.md"), []byte("# B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = []config.ChatSession{
			{ID: "sess_a", WorkspaceDir: dirA},
			{ID: "sess_b", WorkspaceDir: dirB},
		}
	}); err != nil {
		t.Fatal(err)
	}

	a, err := svc.OpenForSession("sess_a", "same.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.OpenForSession("sess_b", "same.md")
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "# A" || b.Text != "# B" {
		t.Fatalf("session previews leaked: a=%q b=%q", a.Text, b.Text)
	}
	if _, err := svc.OpenForSession("sess_a", "../b/same.md"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestTaskWorkspaceSnapshotListsFilesSafely(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	sessionDir := filepath.Join(ws, "tasks", "sess_snapshot")
	for _, dir := range []string{
		filepath.Join(sessionDir, "docs"),
		filepath.Join(sessionDir, "node_modules", "pkg"),
		filepath.Join(sessionDir, ".git"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"docs/report.md":            "# report",
		"notes.txt":                 "notes",
		"node_modules/pkg/index.js": "dependency",
		".git/config":               "git",
		".hidden.txt":               "hidden",
		"program.exe":               "binary",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(sessionDir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(sessionDir, "notes.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	mid := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(sessionDir, "program.exe"), mid, mid); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outside, filepath.Join(sessionDir, "escape.txt"))

	if err := svc.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = []config.ChatSession{{ID: "sess_snapshot", WorkspaceDir: sessionDir}}
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.TaskWorkspaceSnapshot("sess_snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Workspace != sessionDir {
		t.Fatalf("workspace = %q, want %q", snap.Workspace, sessionDir)
	}
	var paths []string
	previewable := map[string]bool{}
	for _, f := range snap.Files {
		paths = append(paths, f.Path)
		previewable[f.Path] = f.Previewable
	}
	got := strings.Join(paths, ",")
	if got != "docs/report.md,program.exe,notes.txt" {
		t.Fatalf("files = %s", got)
	}
	if !previewable["docs/report.md"] || previewable["program.exe"] {
		t.Fatalf("previewability = %+v", previewable)
	}
	if snap.Truncated || snap.Discovered != 3 || snap.RefreshedAt.IsZero() {
		t.Fatalf("snapshot metadata = %+v", snap)
	}
}

func TestTaskWorkspaceSnapshotCapsLargeDirectories(t *testing.T) {
	svc, ws := newArtifactFixture(t)
	sessionDir := filepath.Join(ws, "tasks", "sess_large")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSnapshotFiles+1; i++ {
		name := filepath.Join(sessionDir, fmt.Sprintf("f-%05d.txt", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = []config.ChatSession{{ID: "sess_large", WorkspaceDir: sessionDir}}
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.TaskWorkspaceSnapshot("sess_large")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Truncated || snap.Discovered != maxSnapshotFiles+1 || len(snap.Files) != snapshotListLimit {
		t.Fatalf("truncated snapshot = discovered:%d files:%d truncated:%v", snap.Discovered, len(snap.Files), snap.Truncated)
	}
}

func TestTaskWorkspaceSnapshotRejectsUnknownSession(t *testing.T) {
	svc, _ := newArtifactFixture(t)
	if _, err := svc.TaskWorkspaceSnapshot("sess_missing"); err == nil {
		t.Fatal("expected unknown-session error")
	}
}
