package agentkit

import (
	"context"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"google.golang.org/adk/v2/agent"
	remoteagentv2 "google.golang.org/adk/v2/agent/remoteagent/v2"

	"changeme/internal/config"
)

// BuildRemoteAgent creates an ADK agent that proxies a remote A2A agent
// (Agent-To-Agent protocol, a2aproject/a2a-go). The agent card is resolved
// from the advertised URL on each invocation, so remote-side updates are
// picked up without restarting the app.
func BuildRemoteAgent(rc config.RemoteAgentConfig) (agent.Agent, error) {
	if rc.CardURL == "" {
		return nil, fmt.Errorf("远程 Agent %q 缺少 Card URL", rc.Name)
	}
	return remoteagentv2.NewA2A(remoteagentv2.A2AConfig{
		Name:              sanitizeAgentName(rc.Name),
		Description:       firstNonEmpty(rc.Description, rc.Name),
		AgentCardProvider: remoteagentv2.NewAgentCardProvider(rc.CardURL),
	})
}

// RemoteAgentInfo is the resolved agent card summary shown in the UI when
// checking connectivity of a remote A2A agent.
type RemoteAgentInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	URL         string   `json:"url"`
	Streaming   bool     `json:"streaming"`
	Skills      []string `json:"skills"`
}

// CheckRemoteAgent resolves the agent card at the given URL (A2A well-known
// location) and returns a summary; used by the UI "检查连接" action.
func CheckRemoteAgent(ctx context.Context, cardURL string) (*RemoteAgentInfo, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Agent Card: %w", err)
	}
	info := &RemoteAgentInfo{
		Name:        card.Name,
		Description: card.Description,
		Version:     card.Version,
		Streaming:   card.Capabilities.Streaming,
	}
	if len(card.SupportedInterfaces) > 0 {
		info.URL = card.SupportedInterfaces[0].URL
	}
	for _, s := range card.Skills {
		info.Skills = append(info.Skills, s.Name)
	}
	return info, nil
}
