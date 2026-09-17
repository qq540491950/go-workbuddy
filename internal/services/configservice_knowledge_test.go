package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/config"
)

// TestKnowledgeFileManagement covers listing, traversal guarding and deletion
// of workspace knowledge files.
func TestKnowledgeFileManagement(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.WorkspaceDir = workspace }); err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{S: &Services{Store: store}}

	if files, err := svc.ListKnowledgeFiles(); err != nil || len(files) != 0 {
		t.Fatalf("missing knowledge dir should yield empty list, got %v %v", files, err)
	}

	kb := filepath.Join(workspace, "knowledge")
	if err := os.MkdirAll(kb, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"a.md": "alpha",
		"b.md": "beta",
	} {
		if err := os.WriteFile(filepath.Join(kb, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := svc.ListKnowledgeFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %+v", files)
	}

	// Traversal-guarded deletion.
	if err := svc.DeleteKnowledgeFile("../secret.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := svc.DeleteKnowledgeFile("a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(kb, "a.md")); !os.IsNotExist(err) {
		t.Fatal("file still exists after delete")
	}
	files, _ = svc.ListKnowledgeFiles()
	if len(files) != 1 || files[0].Name != "b.md" {
		t.Fatalf("unexpected remaining files: %+v", files)
	}
	if !strings.Contains(files[0].Name, "b") {
		t.Fatal("sanity")
	}
}
