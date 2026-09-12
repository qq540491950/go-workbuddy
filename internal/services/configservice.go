// Package services implements the Wails services exposed to the frontend.
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"changeme/internal/agentkit"
	"changeme/internal/config"
	"changeme/internal/mcpmgr"
)

// Services bundles the shared dependencies for all Wails services.
type Services struct {
	Store *config.Store
	Kit   *agentkit.Kit
	MCP   *mcpmgr.Manager
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ConfigService exposes configuration CRUD to the frontend.
type ConfigService struct{ S *Services }

// NewConfigService creates the service.
func NewConfigService(s *Services) *ConfigService { return &ConfigService{S: s} }

// GetConfig returns the full configuration (API keys included; local app).
func (c *ConfigService) GetConfig() config.Config { return c.S.Store.Get() }

// SaveProvider inserts or updates a model provider.
func (c *ConfigService) SaveProvider(p config.ModelProvider) error {
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if p.ID == "" {
		p.ID = newID("prov_")
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == p.ID {
				cfg.Providers[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Providers = append(cfg.Providers, p)
		}
		if p.IsDefault {
			for i := range cfg.Providers {
				cfg.Providers[i].IsDefault = cfg.Providers[i].ID == p.ID
			}
		} else if len(cfg.Providers) == 1 {
			cfg.Providers[0].IsDefault = true
		}
	})
}

// DeleteProvider removes a provider by id.
func (c *ConfigService) DeleteProvider(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Providers[:0]
		for _, p := range cfg.Providers {
			if p.ID != id {
				out = append(out, p)
			}
		}
		cfg.Providers = out
	})
}

// TestProvider performs a minimal round-trip against the provider with the
// chosen model to verify reachability and credentials.
func (c *ConfigService) TestProvider(p config.ModelProvider, model string) error {
	m, err := agentkit.NewModelFromProvider(p, model)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("ping", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{MaxOutputTokens: 16},
	}
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil {
			return err
		}
		if resp != nil && resp.Content != nil {
			return nil
		}
	}
	return fmt.Errorf("no response from model")
}

// SaveAgent inserts or updates an agent.
func (c *ConfigService) SaveAgent(a config.AgentConfig) error {
	if a.Name == "" {
		return fmt.Errorf("agent name is required")
	}
	if a.ID == "" {
		a.ID = newID("agent_")
		a.CreatedAt = nowUTC()
	}
	if a.SkillIDs == nil {
		a.SkillIDs = []string{}
	}
	if a.MCPServerIDs == nil {
		a.MCPServerIDs = []string{}
	}
	if a.BuiltinTools == nil {
		a.BuiltinTools = []string{}
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Agents {
			if cfg.Agents[i].ID == a.ID {
				a.CreatedAt = cfg.Agents[i].CreatedAt
				cfg.Agents[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Agents = append(cfg.Agents, a)
		}
	})
}

// DeleteAgent removes an agent and detaches it from teams.
func (c *ConfigService) DeleteAgent(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Agents[:0]
		for _, a := range cfg.Agents {
			if a.ID != id {
				out = append(out, a)
			}
		}
		cfg.Agents = out
		for i := range cfg.Teams {
			ids := cfg.Teams[i].MemberAgentIDs[:0]
			for _, mid := range cfg.Teams[i].MemberAgentIDs {
				if mid != id {
					ids = append(ids, mid)
				}
			}
			cfg.Teams[i].MemberAgentIDs = ids
			if cfg.Teams[i].LeadAgentID == id {
				cfg.Teams[i].LeadAgentID = ""
				cfg.Teams[i].AutoCoordinate = true
			}
		}
	})
}

// SaveTeam inserts or updates an agent team.
func (c *ConfigService) SaveTeam(t config.TeamConfig) error {
	if t.Name == "" {
		return fmt.Errorf("team name is required")
	}
	if t.ID == "" {
		t.ID = newID("team_")
		t.CreatedAt = nowUTC()
	}
	if t.MemberAgentIDs == nil {
		t.MemberAgentIDs = []string{}
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Teams {
			if cfg.Teams[i].ID == t.ID {
				t.CreatedAt = cfg.Teams[i].CreatedAt
				cfg.Teams[i] = t
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Teams = append(cfg.Teams, t)
		}
	})
}

// DeleteTeam removes a team by id.
func (c *ConfigService) DeleteTeam(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Teams[:0]
		for _, t := range cfg.Teams {
			if t.ID != id {
				out = append(out, t)
			}
		}
		cfg.Teams = out
	})
}

// SaveSkill inserts or updates a skill (materialized to SKILL.md).
func (c *ConfigService) SaveSkill(s config.SkillConfig) error {
	if s.Name == "" {
		return fmt.Errorf("skill name is required")
	}
	if s.ID == "" {
		s.ID = newID("skill_")
		s.CreatedAt = nowUTC()
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Skills {
			if cfg.Skills[i].ID == s.ID {
				s.CreatedAt = cfg.Skills[i].CreatedAt
				cfg.Skills[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Skills = append(cfg.Skills, s)
		}
	})
	return err
}

// DeleteSkill removes a skill by id.
func (c *ConfigService) DeleteSkill(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Skills[:0]
		for _, s := range cfg.Skills {
			if s.ID != id {
				out = append(out, s)
			}
		}
		cfg.Skills = out
		for i := range cfg.Agents {
			ids := cfg.Agents[i].SkillIDs[:0]
			for _, sid := range cfg.Agents[i].SkillIDs {
				if sid != id {
					ids = append(ids, sid)
				}
			}
			cfg.Agents[i].SkillIDs = ids
		}
	})
}

// SaveMCPServer inserts or updates an MCP server config.
func (c *ConfigService) SaveMCPServer(s config.MCPServerConfig) error {
	if s.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if s.ID == "" {
		s.ID = newID("mcp_")
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.MCPServers {
			if cfg.MCPServers[i].ID == s.ID {
				cfg.MCPServers[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.MCPServers = append(cfg.MCPServers, s)
		}
	})
}

// DeleteMCPServer removes an MCP server and detaches it from agents.
func (c *ConfigService) DeleteMCPServer(id string) error {
	c.S.MCP.Disconnect(id)
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.MCPServers[:0]
		for _, s := range cfg.MCPServers {
			if s.ID != id {
				out = append(out, s)
			}
		}
		cfg.MCPServers = out
		for i := range cfg.Agents {
			ids := cfg.Agents[i].MCPServerIDs[:0]
			for _, mid := range cfg.Agents[i].MCPServerIDs {
				if mid != id {
					ids = append(ids, mid)
				}
			}
			cfg.Agents[i].MCPServerIDs = ids
		}
	})
}

// SaveSettings persists global settings.
func (c *ConfigService) SaveSettings(s config.Settings) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings = s
	})
}
