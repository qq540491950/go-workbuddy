package agentkit

import (
	"os"
	"path/filepath"
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
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []string{"m1"},
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
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []string{"m1"},
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
	if ts := kit.skillToolset([]string{"s1"}, store.Get()); ts == nil {
		t.Fatal("expected skill toolset, got nil")
	}
	// The skill must be materialized as valid SKILL.md.
	path := filepath.Join(store.SkillsDir(), "weekly-report", "SKILL.md")
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
}

func TestAgentNameUniquenessAcrossBuilds(t *testing.T) {
	a := sanitizeAgentName("周报助手")
	b := sanitizeAgentName("周报助手")
	if a != b {
		t.Fatalf("non-ascii names must map deterministically: %q vs %q", a, b)
	}
}
