package services

import (
	"testing"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

// TestSaveMCPServerNormalizesWhitespace: UI-pasted whitespace must not reach
// the persisted config — exec on "npx " fails LookPath even though npx is
// installed.
func TestSaveMCPServerNormalizesWhitespace(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mcpmgr.New()
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mgr), MCP: mgr}
	cs := NewConfigService(svc)

	err = cs.SaveMCPServer(config.MCPServerConfig{
		ID:        "mcp_1",
		Name:      "  tavily  ",
		Transport: config.MCPStdio,
		Command:   " npx ",
		Args:      []string{" -y ", "tavily-mcp"},
		URL:       " https://example.com ",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got config.MCPServerConfig
	for _, s := range store.Get().MCPServers {
		if s.ID == "mcp_1" {
			got = s
		}
	}
	if got.Command != "npx" || got.Name != "tavily" || got.URL != "https://example.com" {
		t.Fatalf("fields not trimmed: command=%q name=%q url=%q", got.Command, got.Name, got.URL)
	}
	if len(got.Args) != 2 || got.Args[0] != "-y" || got.Args[1] != "tavily-mcp" {
		t.Fatalf("args not trimmed: %q", got.Args)
	}
}
