package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"go-workbuddy/internal/agentkit"
)

// RememberSession ingests the whole conversation into long-term memory so
// other sessions (with the memory tool) can recall it later.
func (c *ChatService) RememberSession(sessionID string) (int, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return 0, err
	}
	if resp.Session == nil {
		return 0, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	if c.S.Memory == nil {
		return 0, fmt.Errorf("记忆服务不可用")
	}
	if err := c.S.Memory.AddSessionToMemory(context.Background(), resp.Session); err != nil {
		return 0, err
	}
	n, err := c.S.Memory.Count(c.appName, c.userID)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// SessionStats aggregates observability metrics for one conversation.
type SessionStats struct {
	Messages       int          `json:"messages"`
	ToolCalls      int          `json:"toolCalls"`
	ToolFailures   int          `json:"toolFailures"`
	AssistantTurns int          `json:"assistantTurns"`
	TokensIn       int64        `json:"tokensIn"`
	TokensOut      int64        `json:"tokensOut"`
	TokensTotal    int64        `json:"tokensTotal"`
	Daily          []StatDay    `json:"daily,omitempty"`   // per-day token/cost breakdown
	Authors        []AuthorStat `json:"authors,omitempty"` // per-participant breakdown (team timelines)
	FirstAt        *time.Time   `json:"firstAt,omitempty"`
	LastAt         *time.Time   `json:"lastAt,omitempty"`
}

// AuthorStat breaks usage down per conversation participant (team members,
// remote agents); "user" is excluded.
type AuthorStat struct {
	Author    string `json:"author"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"toolCalls"`
	Tokens    int64  `json:"tokens"`
}

// StatDay is one calendar day of usage with an estimated cost using the
// session target's current model prices (¥/1M tokens).
type StatDay struct {
	Date             string  `json:"date"` // YYYY-MM-DD (local)
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	EstimatedCost    float64 `json:"estimatedCost"`
}

// SessionStats computes aggregate metrics from the stored ADK events.
func (c *ChatService) SessionStats(sessionID string) (*SessionStats, error) {
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return nil, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	st := &SessionStats{}
	if resp.Session == nil {
		return st, nil
	}
	// Model prices (¥/1M tokens) come from the session target's CURRENT
	// model; historical turns that used another model are approximated.
	priceIn, priceOut := 0.0, 0.0
	cfgNow := c.S.Store.Get()
	priceAc := c.targetAgentConfig(cfgNow, target)
	if priceAc == nil && target.TargetType == "" {
		if d, derr := directAgentConfig(cfgNow); derr == nil {
			priceAc = &d
		}
	}
	if ac := priceAc; ac != nil {
		// Pricing follows the model the session actually runs (same clamp as
		// runTurn: an override whose model was deleted falls back to the
		// agent's configured model).
		effectiveModel := agentkit.EffectiveModel(cfgNow, ac.ProviderID, ac.Model, target.ModelOverride)
		for i := range cfgNow.Providers {
			if cfgNow.Providers[i].ID != ac.ProviderID {
				continue
			}
			for _, mi := range cfgNow.Providers[i].Models {
				if mi.ID == effectiveModel {
					priceIn, priceOut = mi.PriceIn, mi.PriceOut
				}
			}
			break
		}
	}
	days := map[string]*StatDay{}
	authors := map[string]*AuthorStat{}

	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		first, last := st.FirstAt, st.LastAt
		if first == nil || ev.Timestamp.Before(*first) {
			ts := ev.Timestamp
			st.FirstAt = &ts
		}
		if last == nil || ev.Timestamp.After(*last) {
			ts := ev.Timestamp
			st.LastAt = &ts
		}
		if ev.Author == "user" {
			st.Messages++
			continue
		}
		if ev.Content == nil {
			continue
		}
		if u := usageOf(ev); u != nil {
			st.TokensIn += u.PromptTokens
			st.TokensOut += u.CompletionTokens
			st.TokensTotal += u.TotalTokens

			as := authors[ev.Author]
			if as == nil {
				as = &AuthorStat{Author: ev.Author}
				authors[ev.Author] = as
			}
			as.Tokens += u.TotalTokens

			day := ev.Timestamp.Local().Format("2006-01-02")
			d := days[day]
			if d == nil {
				d = &StatDay{Date: day}
				days[day] = d
			}
			d.PromptTokens += u.PromptTokens
			d.CompletionTokens += u.CompletionTokens
			d.TotalTokens += u.TotalTokens
			d.EstimatedCost += float64(u.PromptTokens)/1e6*priceIn + float64(u.CompletionTokens)/1e6*priceOut
		}
		if ev.Partial {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				st.ToolCalls++
				if as := authors[ev.Author]; as != nil {
					as.ToolCalls++
				}
			case p.FunctionResponse != nil:
				if strings.Contains(string(mustJSON(p.FunctionResponse.Response)), `"error"`) {
					st.ToolFailures++
				}
			}
		}
		if ev.TurnComplete && !ev.Partial {
			st.AssistantTurns++
			if as := authors[ev.Author]; as != nil {
				as.Turns++
			}
		}
	}
	for name, as := range authors {
		as.Author = name
		st.Authors = append(st.Authors, *as)
	}
	sort.Slice(st.Authors, func(i, j int) bool { return st.Authors[i].Author < st.Authors[j].Author })
	for _, d := range days {
		st.Daily = append(st.Daily, *d)
	}
	sort.Slice(st.Daily, func(i, j int) bool { return st.Daily[i].Date < st.Daily[j].Date })
	return st, nil
}
