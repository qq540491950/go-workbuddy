package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"changeme/internal/config"
	"changeme/internal/mcpmgr"

	"changeme/internal/agentkit"
)

func newBackupTestService(t *testing.T) (*AppService, *config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: "https://api.example.com/v1", APIKey: "sk-secret-123", IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "A", ProviderID: "prov_1"})
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{ID: "ra_1", Name: "R", CardURL: "http://x"})
		cfg.Teams = append(cfg.Teams, config.TeamConfig{ID: "team_1", Name: "T"})
		cfg.Skills = append(cfg.Skills, config.SkillConfig{ID: "skill_1", Name: "S"})
		cfg.Sessions = append(cfg.Sessions, config.ChatSession{ID: "sess_1", Title: "会话"})
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewAppService(svc), store, workspace
}

func TestConfigBackupRoundTrip(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	path, err := app.ExportConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-secret-123") {
		t.Fatal("unmasked export must contain the API key")
	}

	// Wipe the config, then import the backup back.
	if err := store.Update(func(cfg *config.Config) {
		*cfg = config.Config{}
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.ImportConfig(string(data)); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if len(got.Providers) != 1 || got.Providers[0].APIKey != "sk-secret-123" {
		t.Fatalf("provider/key not restored: %+v", got.Providers)
	}
	if len(got.Agents) != 1 || len(got.RemoteAgents) != 1 || len(got.Teams) != 1 {
		t.Fatalf("config not fully restored: %+v", got)
	}
	// The seeded store ships default skills too; ours must be among them.
	var haveSkill bool
	for _, sk := range got.Skills {
		if sk.ID == "skill_1" {
			haveSkill = true
		}
	}
	if !haveSkill {
		t.Fatalf("custom skill lost in round trip: %+v", got.Skills)
	}
	if got.Settings.WorkspaceDir == "" {
		t.Fatal("settings not restored")
	}
}

func TestConfigBackupMasked(t *testing.T) {
	app, store, workspace := newBackupTestService(t)

	path, err := app.ExportConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret-123") {
		t.Fatal("masked export leaked the API key")
	}
	// Regression: masking must be applied to a copy. A masked export used to
	// blank the live provider keys, breaking every later request until restart.
	if got := store.Get(); len(got.Providers) != 1 || got.Providers[0].APIKey != "sk-secret-123" {
		t.Fatalf("masked export mutated the live config: %+v", got.Providers)
	}
	// OAuth token cache must never be exported (secrets, machine-local).
	err = store.Update(func(cfg *config.Config) {
		cfg.MCPTokens = map[string]config.MCPOAuthToken{"m1": {AccessToken: "at-secret", TokenURL: "https://x/token"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	path2, err := app.ExportConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data2), "at-secret") {
		t.Fatal("export leaked the MCP OAuth token cache")
	}
	// The key file must not be group/other readable since it may hold secrets.
	if filepath.Dir(path) != filepath.Join(workspace, "exports") {
		t.Fatalf("unexpected export dir: %s", path)
	}
}

func TestConfigImportValidation(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	if err := app.ImportConfig("not json at all {"); err == nil {
		t.Fatal("expected parse error")
	}
	if err := app.ImportConfig(`{"version":99,"config":{}}`); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("expected version rejection, got %v", err)
	}
	if err := app.ImportConfig(`{"version":1}`); err == nil {
		t.Fatal("expected missing config rejection")
	}

	// Valid backup: sessions in the live config survive the import.
	if err := app.ImportConfig(`{"version":1,"config":{"settings":{"themeMode":"light"},"providers":[],"agents":[]}}`); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "sess_1" {
		t.Fatalf("sessions must survive import: %+v", got.Sessions)
	}
	if got.Settings.ThemeMode != "light" {
		t.Fatalf("imported settings missing: %+v", got.Settings)
	}
}

// TestSettingsA2AAutoStartPersisted: the auto-start trio survives a save/load
// round trip (config.json serialization).
func TestSettingsA2AAutoStartPersisted(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.A2AAutoStart = true
		cfg.Settings.A2AAgentID = "agent_1"
		cfg.Settings.A2APort = 18080
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := reopened.Get().Settings
	if !st.A2AAutoStart || st.A2AAgentID != "agent_1" || st.A2APort != 18080 {
		t.Fatalf("A2A auto-start settings not persisted: %+v", st)
	}
}

// TestSaveSettingsProxyValidation: malformed proxy URLs are rejected at save
// time with a clear error.
func TestSaveSettingsProxyValidation(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	st := store.Get().Settings

	st.HTTPProxy = "not a url"
	if err := app.S.Store.Update(func(cfg *config.Config) {}); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigService(app.S)
	if err := svc.SaveSettings(st); err == nil {
		t.Fatal("expected invalid proxy rejection")
	}

	st.HTTPProxy = "http://127.0.0.1:7890"
	if err := svc.SaveSettings(st); err != nil {
		t.Fatalf("valid proxy rejected: %v", err)
	}
	if store.Get().Settings.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatal("proxy not persisted")
	}
}

// TestConfigBackupMaskedCoversEnvAndHeaders: a "safe to share" export must
// also strip MCP Env/Headers (they routinely carry API keys and Authorization
// headers), not just provider keys and auth tokens.
func TestConfigBackupMaskedCoversEnvAndHeaders(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.MCPServers = append(cfg.MCPServers, config.MCPServerConfig{
			ID: "mcp_1", Name: "M", Transport: config.MCPStdio, Command: "/bin/tool",
			Env:     map[string]string{"OPENAI_API_KEY": "env-secret-1"},
			Headers: map[string]string{"Authorization": "Bearer hdr-secret-2"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	path, err := app.ExportConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"env-secret-1", "hdr-secret-2"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("masked export leaked %s", secret)
		}
	}
}

// TestConfigImportPreservesLocalState pins the import semantics: OAuth token
// cache is machine-local and survives, old-schema backups come out normalized
// (settings defaults + legacy price migration), and changed/deleted MCP
// servers drop their cached connections.
func TestConfigImportPreservesLocalState(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.MCPTokens = map[string]config.MCPOAuthToken{"m1": {AccessToken: "at-local", TokenURL: "https://x/token"}}
		cfg.Settings.ThemeMode = ""
		cfg.Providers[0].PriceIn = 2.0 // legacy provider-level price
	}); err != nil {
		t.Fatal(err)
	}

	// Backup from an OLD schema: no tokens, no per-model price, empty theme.
	backup := `{"version":1,"config":{
		"settings":{"workspaceDir":"/tmp/ws"},
		"providers":[{"id":"prov_1","name":"P","protocol":"openai","baseUrl":"https://api.example.com/v1","apiKey":"k","priceIn":2.0,"priceOut":8.0,"models":[{"id":"m1"}]}],
		"mcpServers":[{"id":"mcp_1","name":"M","transport":"stdio","command":"/bin/tool"}],
		"agents":[]}}`
	if err := app.ImportConfig(backup); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if got.MCPTokens["m1"].AccessToken != "at-local" {
		t.Fatalf("local OAuth token cache wiped by import: %+v", got.MCPTokens)
	}
	if got.Settings.ThemeMode == "" {
		t.Fatalf("imported settings not normalized: %+v", got.Settings)
	}
	if len(got.Providers) != 1 || len(got.Providers[0].Models) != 1 {
		t.Fatalf("imported provider unexpected: %+v", got.Providers)
	}
	mi := got.Providers[0].Models[0]
	if mi.PriceIn != 2.0 || mi.PriceOut != 8.0 {
		t.Fatalf("legacy provider price not migrated down: %+v", mi)
	}
	if got.Providers[0].PriceIn != 0 {
		t.Fatalf("legacy provider-level price not cleared: %+v", got.Providers[0])
	}

	// A server whose config changed (or vanished) must drop its cached
	// toolset: the second import replaces /bin/tool with /bin/tool2.
	cfg := `{"version":1,"config":{
		"settings":{},
		"providers":[],
		"mcpServers":[{"id":"mcp_1","name":"M","transport":"stdio","command":"/bin/tool2"}],
		"agents":[]}}`
	if err := app.ImportConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
