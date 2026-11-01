package services

import (
	"os"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

// TestAgentShareRoundTrip exports an agent and re-imports it (twice) to
// verify fresh IDs, name-conflict suffixing and provider relinking metadata.
func TestAgentShareRoundTrip(t *testing.T) {
	app, store, workspace := newBackupTestService(t)

	path, err := app.ExportAgent("agent_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "agents") {
		t.Fatalf("unexpected export path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	firstRes, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	first := firstRes.Agent
	if first.ID == "agent_1" || first.ID == "" {
		t.Fatalf("imported agent must get a fresh ID: %q", first.ID)
	}
	// The original "A" still exists, so the first import already gets a suffix.
	if first.Name != "A（2）" {
		t.Fatalf("name conflict suffix missing on first import: %q", first.Name)
	}

	secondRes, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	second := secondRes.Agent
	if second.Name != "A（3）" {
		t.Fatalf("name conflict suffix missing: %q", second.Name)
	}
	if second.ID == first.ID {
		t.Fatal("imported agents must have distinct IDs")
	}

	// Unknown agent export errors.
	if _, err := app.ExportAgent("nope"); err == nil {
		t.Fatal("expected error exporting unknown agent")
	}
	// Bad share data rejected.
	if _, err := app.ImportAgent("junk{"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := app.ImportAgent(`{"version":9,"agent":{"name":"x"}}`); err == nil {
		t.Fatal("expected version rejection")
	}
	_ = store
	_ = workspace
}

// TestImportAgentStripsDanglingRefs: skill / MCP references that do not
// exist on THIS machine are stripped at import (with a warning) — otherwise
// every turn of the imported agent fails its build outright.
func TestImportAgentStripsDanglingRefs(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Skills {
			if cfg.Skills[i].ID == "skill_1" {
				cfg.Skills[i].Description = "fixture skill"
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	data := `{"version":1,"agent":{` +
		`"id":"agent_x","name":"访客","description":"d","providerId":"prov_1",` +
		`"model":"m",` +
		`"skillIds":["skill_1","skill_missing"],` +
		`"mcpServerIds":["mcp_missing"],` +
		`"builtinTools":["time"]}}`
	res, err := app.ImportAgent(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agent.SkillIDs) != 1 || res.Agent.SkillIDs[0] != "skill_1" {
		t.Fatalf("dangling skill ref not stripped: %v", res.Agent.SkillIDs)
	}
	if len(res.Agent.MCPServerIDs) != 0 {
		t.Fatalf("dangling MCP ref not stripped: %v", res.Agent.MCPServerIDs)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", res.Warnings)
	}
	if _, err := app.S.Kit.BuildAgent(res.Agent); err != nil {
		t.Fatalf("imported agent must build after cleanup: %v", err)
	}
}

// TestRemoteAgentShareRoundTrip mirrors the agent share flow for remote A2A
// agents: export -> import with fresh ID, conflict suffix, validation.
func TestRemoteAgentShareRoundTrip(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	path, err := app.ExportRemoteAgent("ra_1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := app.ImportRemoteAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "ra_1" || first.ID == "" {
		t.Fatalf("imported remote must get a fresh ID: %q", first.ID)
	}
	if first.Name != "R（2）" {
		t.Fatalf("name conflict suffix missing: %q", first.Name)
	}
	_ = store
	if _, err := app.ExportRemoteAgent("missing"); err == nil {
		t.Fatal("expected error exporting unknown remote agent")
	}
}
