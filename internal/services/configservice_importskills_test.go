package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

// TestImportSkillsFromWorkspace covers the agentskills.io layout import:
// frontmatter parsing, duplicate-name skipping, non-SKILL files ignored and
// the no-directory error path.
// TestImportSkillsRejectsNormalizedCollisions: names that normalize to the
// same materialized directory (CJK names all collapse to the same bucket)
// must conflict at import, not silently overwrite each other's SKILL.md.
func TestImportSkillsRejectsNormalizedCollisions(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
	}); err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{S: &Services{Store: store}}
	skillsDir := filepath.Join(workspace, "skills")
	mk := func(dirName, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(skillsDir, dirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillsDir, dirName, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", "---\nname: 周报\ndescription: 周报技能\n---\n\n正文一。")
	mk("b", "---\nname: 指南\ndescription: 指南技能\n---\n\n正文二。")

	n, err := svc.ImportSkillsFromWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("normalized collision must import only one skill, got %d", n)
	}
	var importedCount int
	for _, s := range store.Get().Skills {
		if s.Name == "周报" || s.Name == "指南" {
			importedCount++
		}
	}
	if importedCount != 1 {
		t.Fatalf("exactly one of the colliding skills may be stored, got %d", importedCount)
	}
}

func TestImportSkillsFromWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
	}); err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{S: &Services{Store: store}}

	if _, err := svc.ImportSkillsFromWorkspace(); err == nil {
		t.Fatal("expected error when workspace has no skills dir")
	}

	skillsDir := filepath.Join(workspace, "skills")
	mk := func(dirName, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(skillsDir, dirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillsDir, dirName, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("translate", "---\nname: translate\ndescription: 中英互译\n---\n\n# 翻译规则\n\n正文内容。")
	mk("weekly", "# 周报助手\n\n无 frontmatter，用一级标题命名。")
	mk("translate-dup", "---\nname: Translate\n---\n\n重复名（大小写不同）应跳过。")
	mk("ignored.txt", "不是 SKILL.md")
	if err := os.MkdirAll(filepath.Join(skillsDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	n, err := svc.ImportSkillsFromWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 imported (translate + 周报助手), got %d", n)
	}

	// Idempotent: second import adds nothing (name duplicate detection).
	n2, err := svc.ImportSkillsFromWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("re-import must skip existing names, got %d", n2)
	}

	cfg := store.Get()
	var foundTranslate, foundWeekly bool
	for _, sk := range cfg.Skills {
		if strings.EqualFold(sk.Name, "translate") && sk.Description == "中英互译" && strings.Contains(sk.Content, "正文内容") {
			foundTranslate = true
		}
		if sk.Name == "周报助手" {
			foundWeekly = true
		}
	}
	if !foundTranslate || !foundWeekly {
		t.Fatalf("imported skills incomplete: translate=%v weekly=%v", foundTranslate, foundWeekly)
	}
}

// TestSaveSkillRejectsNormalizedCollision: "API Review" and "api-review"
// materialize to the same directory; the second save must fail instead of
// silently overwriting the first skill.
func TestSaveSkillRejectsNormalizedCollision(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	cs := NewConfigService(svc)
	if err := cs.SaveSkill(config.SkillConfig{ID: "s1", Name: "API Review", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SaveSkill(config.SkillConfig{ID: "s2", Name: "api-review", Content: "b"}); err == nil {
		t.Fatal("expected normalized-name collision rejection")
	}
	// Re-saving the same skill (same ID) is fine.
	if err := cs.SaveSkill(config.SkillConfig{ID: "s1", Name: "api-review", Content: "a2"}); err != nil {
		t.Fatalf("self-update rejected: %v", err)
	}
}
