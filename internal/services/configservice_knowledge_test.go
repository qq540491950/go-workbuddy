package services

import (
	"os"
	"path/filepath"
	"strings"
	"time"
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
	now := time.Now()
	for name, content := range map[string]string{
		"a.md": "alpha",
		"b.md": "beta",
	} {
		if err := os.WriteFile(filepath.Join(kb, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct mtimes: b.md written last => newest first in the listing.
	if err := os.Chtimes(filepath.Join(kb, "a.md"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(kb, "b.md"), now, now); err != nil {
		t.Fatal(err)
	}

	files, err := svc.ListKnowledgeFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %+v", files)
	}
	// b.md written last => newest first.
	if files[0].Name != "b.md" {
		t.Fatalf("expected newest first: %+v", files)
	}

	// Nested files are listed with slash-separated relative paths.
	nested := filepath.Join(kb, "docs", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.md"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err = svc.ListKnowledgeFiles()
	if err != nil {
		t.Fatal(err)
	}
	var foundNested bool
	for _, f := range files {
		if f.Name == filepath.ToSlash(filepath.Join("docs", "sub", "deep.md")) {
			foundNested = true
		}
	}
	if !foundNested {
		t.Fatalf("nested file missing from listing: %+v", files)
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
	if len(files) != 2 {
		t.Fatalf("unexpected remaining files: %+v", files)
	}
	for _, f := range files {
		if f.Name != "b.md" && f.Name != "docs/sub/deep.md" {
			t.Fatalf("unexpected file: %+v", f)
		}
	}
	if !strings.Contains(files[0].Name, "b") {
		t.Fatal("sanity")
	}
}

// TestCreateKnowledgeFile: creation succeeds, refuses duplicates, refuses
// traversal names, and refuses empty content.
func TestCreateKnowledgeFile(t *testing.T) {
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

	name, err := svc.CreateKnowledgeFile("notes.md", "第一条知识")
	if err != nil {
		t.Fatal(err)
	}
	if name != "notes.md" {
		t.Fatalf("unexpected name: %q", name)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "knowledge", "notes.md"))
	if err != nil || string(data) != "第一条知识" {
		t.Fatalf("file content wrong: %q %v", data, err)
	}
	if _, err := svc.CreateKnowledgeFile("notes.md", "again"); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	if _, err := svc.CreateKnowledgeFile("../evil.md", "x"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := svc.CreateKnowledgeFile("empty.md", "  "); err == nil {
		t.Fatal("expected empty content rejection")
	}
}
