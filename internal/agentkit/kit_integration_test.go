package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool/skilltoolset/skill"

	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// newTestKit builds a Kit over a temp config store.
func newTestKit(t *testing.T) (*Kit, *config.Store) {
	t.Helper()
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewKit(store, mcpmgr.New()), store
}

func TestBuildAgentWithBuiltinTools(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手 A", ProviderID: "p1", Model: "m1",
			SystemPrompt: "你好 {不是状态占位符} 可以包含大括号",
			BuiltinTools: []string{"time"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	a, err := kit.BuildAgent(cfg.Agents[0])
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() == "" {
		t.Fatal("agent name empty")
	}
	if a.Description() != "助手 A" {
		t.Fatalf("unexpected description: %q", a.Description())
	}
}

func TestBuildTeamAutoCoordinator(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		for _, id := range []string{"m1", "m2"} {
			cfg.Agents = append(cfg.Agents, config.AgentConfig{
				ID: id, Name: "成员" + id, ProviderID: "p1", Model: "m1",
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	team := config.TeamConfig{
		ID: "t1", Name: "Dream Team", AutoCoordinate: true,
		MemberAgentIDs: []string{"m1", "m2"},
		ProviderID:     "p1", Model: "m1",
	}
	root, name, err := kit.BuildTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	if name != "dream_team" {
		t.Fatalf("team name = %q", name)
	}
	subs := root.SubAgents()
	if len(subs) != 2 {
		t.Fatalf("expected 2 sub-agents, got %d", len(subs))
	}

	// Explicit lead mode: lead is root, the other member is a peer.
	team2 := team
	team2.AutoCoordinate = false
	team2.LeadAgentID = "m1"
	root2, _, err := kit.BuildTeam(team2)
	if err != nil {
		t.Fatal(err)
	}
	if root2.Name() != sanitizeAgentName("成员m1") {
		t.Fatalf("lead should be root, got %q", root2.Name())
	}
	if len(root2.SubAgents()) != 1 {
		t.Fatalf("lead should have 1 peer, got %d", len(root2.SubAgents()))
	}
}

func TestBuildTeamMissingProvider(t *testing.T) {
	kit, store := newTestKit(t)
	team := config.TeamConfig{ID: "t", Name: "T", AutoCoordinate: true, ProviderID: "nope"}
	if _, _, err := kit.BuildTeam(team); err == nil {
		// store has a default agent list, provider missing -> must error
		_ = store
		t.Fatal("expected error for missing provider")
	}
}

func TestSkillMaterialization(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: "s1", Name: "Weekly Report", Description: "写周报", Content: "# 步骤\n1. 收集",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_x", Name: "Tester", SkillIDs: []string{"s1"}}
	ts, err := kit.skillToolset(ac, store.Get())
	if err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	if ts == nil {
		t.Fatal("expected skill toolset, got nil")
	}
	// The skill must be materialized as valid SKILL.md inside the per-agent
	// staging dir (agents/<id>/<skill>/SKILL.md), not the shared root.
	path := filepath.Join(store.SkillsDir(), "agents", ac.ID, "weekly-report", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := skill.ParseBytes(data)
	if err != nil {
		t.Fatalf("materialized SKILL.md invalid: %v", err)
	}
	if fm.Name != "weekly-report" || fm.Description != "写周报" {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if _, err := os.Stat(filepath.Join(store.SkillsDir(), "weekly-report")); !os.IsNotExist(err) {
		t.Fatalf("skill leaked into the shared skills root: %v", err)
	}
}

// TestSkillToolsetPrunesUnmountedSkills pins the per-agent isolation: a skill
// materialized for one agent must not appear in another agent's toolset, and
// detaching a skill removes its files on the next build.
func TestSkillToolsetPrunesUnmountedSkills(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Skills = append(cfg.Skills,
			config.SkillConfig{ID: "s1", Name: "Shared Skill", Description: "技能一", Content: "one"},
			config.SkillConfig{ID: "s2", Name: "Private Skill", Description: "技能二", Content: "two"},
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	mountAll := config.AgentConfig{ID: "agent_a", Name: "A", SkillIDs: []string{"s1", "s2"}}
	mountOne := config.AgentConfig{ID: "agent_b", Name: "B", SkillIDs: []string{"s1"}}
	if _, err := kit.skillToolset(mountAll, store.Get()); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.skillToolset(mountOne, store.Get()); err != nil {
		t.Fatal(err)
	}
	// Agent B's staging dir must only contain the mounted skill.
	entries, err := os.ReadDir(filepath.Join(store.SkillsDir(), "agents", "agent_b"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "shared-skill" {
		t.Fatalf("agent B staging dir = %v, want [shared-skill]", names)
	}
	// Detaching s2 from agent A prunes it on the next build.
	mountAll.SkillIDs = []string{"s1"}
	if _, err := kit.skillToolset(mountAll, store.Get()); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(store.SkillsDir(), "agents", "agent_a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "shared-skill" {
		names = nil
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("agent A staging dir after detach = %v, want [shared-skill]", names)
	}
	// RemoveMaterializedSkill clears every agent's copy of a deleted skill.
	RemoveMaterializedSkill(store.SkillsDir(), "Shared Skill")
	for _, id := range []string{"agent_a", "agent_b"} {
		if _, err := os.Stat(filepath.Join(store.SkillsDir(), "agents", id, "shared-skill")); !os.IsNotExist(err) {
			t.Fatalf("agent %s still has shared-skill after removal: %v", id, err)
		}
	}
}

func TestAgentNameUniquenessAcrossBuilds(t *testing.T) {
	a := sanitizeAgentName("周报助手")
	b := sanitizeAgentName("周报助手")
	if a != b {
		t.Fatalf("non-ascii names must map deterministically: %q vs %q", a, b)
	}
}

// TestBuildAgentReportsMCPFailures pins the no-silent-skip contract: a server
// whose transport cannot be built and a dangling server ID both surface in the
// build error instead of quietly running an agent without its tools.
func TestBuildAgentReportsMCPFailures(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.MCPServers = append(cfg.MCPServers, config.MCPServerConfig{
			ID: "mcp_bad", Name: "坏服务器", Transport: config.MCPStdio, Command: "",
		})
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_m", Name: "工具人", ProviderID: "p1", Model: "m1",
		MCPServerIDs: []string{"mcp_bad", "mcp_missing"}}
	_, err := kit.BuildAgent(ac)
	if err == nil {
		t.Fatal("expected aggregated MCP error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "坏服务器") {
		t.Fatalf("error missing broken server: %v", msg)
	}
	if !strings.Contains(msg, "mcp_missing") {
		t.Fatalf("error missing dangling server id: %v", msg)
	}
}

// TestBuildTeamLeadPeersIncludeRemoteMembers pins the explicit-lead path: the
// lead's transferable peers must include local AND remote members (the remote
// ones used to be silently dropped), with dangling member IDs skipped instead
// of shifting positional indexes.
func TestBuildTeamLeadPeersIncludeRemoteMembers(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.Agents = append(cfg.Agents,
			config.AgentConfig{ID: "a_lead", Name: "组长", ProviderID: "p1", Model: "m1"},
			config.AgentConfig{ID: "a_peer", Name: "本地队员", ProviderID: "p1", Model: "m1"},
		)
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_1", Name: "远程队员", CardURL: "http://127.0.0.1:1/card", // lazy: never fetched at build time
		})
	}); err != nil {
		t.Fatal(err)
	}
	team := config.TeamConfig{
		ID: "team_x", Name: "混合队", LeadAgentID: "a_lead",
		MemberAgentIDs: []string{"a_lead", "a_peer", "a_gone"},
		RemoteMemberIDs: []string{"ra_1"},
	}
	root, rootName, err := kit.BuildTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	if rootName != SanitizeAgentName("组长") {
		t.Fatalf("root = %s", rootName)
	}
	peers := root.SubAgents()
	if len(peers) != 2 {
		t.Fatalf("lead peers = %d, want 2 (local + remote, dangling skipped)", len(peers))
	}
	names := map[string]bool{}
	for _, p := range peers {
		names[p.Name()] = true
	}
	if !names[SanitizeAgentName("本地队员")] || !names[SanitizeAgentName("远程队员")] {
		t.Fatalf("peers missing expected members: %v", names)
	}
}
