package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for i, name := range []string{"a.png", "b.md", "c.txt", "d.exe"} {
		_ = os.WriteFile(filepath.Join(ws, name), []byte{byte(i)}, 0o644)
	}
	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	// d.exe has an unknown extension and must be excluded.
	if strings.Join(names, ",") != "a.png,b.md,c.txt" {
		t.Fatalf("list = %v", names)
	}
}
