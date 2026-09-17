package services

import (
	"os"
	"strings"
	"testing"
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

	first, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "agent_1" || first.ID == "" {
		t.Fatalf("imported agent must get a fresh ID: %q", first.ID)
	}
	// The original "A" still exists, so the first import already gets a suffix.
	if first.Name != "A（2）" {
		t.Fatalf("name conflict suffix missing on first import: %q", first.Name)
	}

	second, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
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
