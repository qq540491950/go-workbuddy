package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Defaults are seeded on first run.
	if len(s.Get().Skills) == 0 {
		t.Fatal("expected default skills to be seeded")
	}
	if s.Get().Settings.WorkspaceDir == "" {
		t.Fatal("expected default workspace dir")
	}

	err = s.Update(func(cfg *Config) {
		cfg.Providers = append(cfg.Providers, ModelProvider{
			ID: "p1", Name: "Test", Protocol: ProtocolOpenAI, Models: []string{"m1"}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, AgentConfig{
			ID: "a1", Name: "Agent X", ProviderID: "p1", Model: "m1",
			SkillIDs: []string{}, MCPServerIDs: []string{}, BuiltinTools: []string{"time"},
			CreatedAt: time.Now().UTC(),
		})
		cfg.Sessions = append(cfg.Sessions, ChatSession{
			ID: "s1", Title: "hello", TargetType: "agent", TargetID: "a1",
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reopen: data must persist.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get()
	if len(got.Providers) != 1 || got.Providers[0].Name != "Test" {
		t.Fatalf("providers not persisted: %+v", got.Providers)
	}
	if len(got.Agents) != 1 || got.Agents[0].BuiltinTools[0] != "time" {
		t.Fatalf("agents not persisted: %+v", got.Agents)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Title != "hello" {
		t.Fatalf("sessions not persisted: %+v", got.Sessions)
	}
}

func TestOpenCorruptConfigKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Get().Skills) == 0 {
		t.Fatal("expected fresh defaults after corrupt config")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("expected .bak backup of corrupt config")
	}
}

func TestSkillsDir(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.SkillsDir() == "" || filepath.Base(s.SkillsDir()) != "skills" {
		t.Fatalf("unexpected skills dir: %q", s.SkillsDir())
	}
}
