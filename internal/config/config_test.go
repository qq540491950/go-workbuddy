package config

import (
	"encoding/json"
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
			ID: "p1", Name: "Test", Protocol: ProtocolOpenAI, Models: []ModelInfo{{ID: "m1"}}, IsDefault: true,
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

func TestLegacyModelsAndPricesMigrate(t *testing.T) {
	dir := t.TempDir()
	// Old schema: models are plain strings; prices live on the provider.
	legacy := `{
	  "settings": {"workspaceDir": "/tmp/w", "language": "zh-CN"},
	  "providers": [
	    {"id":"p1","name":"MiniMax","protocol":"anthropic",
	     "baseUrl":"https://api.minimaxi.com/anthropic","apiKey":"k",
	     "models":["MiniMax-M3",{"id":"MiniMax-M2","multimodal":true,"priceIn":2,"priceOut":8}],
	     "isDefault":true,"priceIn":1,"priceOut":4}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Get().Providers[0]
	if len(p.Models) != 2 || p.Models[0].ID != "MiniMax-M3" || p.Models[1].ID != "MiniMax-M2" {
		t.Fatalf("models not migrated: %+v", p.Models)
	}
	if p.Models[0].PriceIn != 1 || p.Models[0].PriceOut != 4 {
		t.Fatalf("provider price not copied down: %+v", p.Models[0])
	}
	if !p.Models[1].Multimodal || p.Models[1].PriceIn != 2 || p.Models[1].PriceOut != 8 {
		t.Fatalf("explicit model metadata overwritten: %+v", p.Models[1])
	}
	if p.PriceIn != 0 || p.PriceOut != 0 {
		t.Fatalf("legacy provider prices not cleared: %+v", p)
	}

	// Migration is persisted: reopening must not lose or duplicate it.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p2 := s2.Get().Providers[0]
	if p2.Models[0].PriceIn != 1 || p2.Models[0].PriceOut != 4 || p2.PriceIn != 0 {
		t.Fatalf("migration not persisted: %+v", p2)
	}
}

func TestModelInfoUnmarshalRejectsBadObject(t *testing.T) {
	var m ModelInfo
	if err := json.Unmarshal([]byte(`{"id":"x","contextWindow":128000}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "x" || m.ContextWindow != 128000 {
		t.Fatalf("got %+v", m)
	}
	// Unknown fields are ignored; the nameless entry is dropped by migrateLegacy.
	if err := json.Unmarshal([]byte(`{"nope":1}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "" {
		t.Fatalf("got %+v, want empty entry", m)
	}
}
