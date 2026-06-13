package floor

import (
	"context"
	"fmt"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// RunOnceConfig configures a single-agent, single-turn run.
type RunOnceConfig struct {
	Blueprint *blueprint.Blueprint // floor blueprint (furniture, agent config)
	AgentID   string               // which agent to run
	Prompt    string               // override the agent's system prompt (empty = use blueprint prompt)
	Input     string               // the user input to post
}

// RunOnceResult holds the agent's response from a single turn.
type RunOnceResult struct {
	Content          string
	ToolInteractions []ToolInteraction
}

// RunOnce starts a floor, runs one agent against one input, and returns the response.
// No controller, no multi-agent turn-taking — just: start floor, run agent, get response.
func RunOnce(cfg RunOnceConfig) (*RunOnceResult, error) {
	// Find the agent definition in the blueprint
	var agentDef *blueprint.Agent
	for i := range cfg.Blueprint.Agents {
		if cfg.Blueprint.Agents[i].ID == cfg.AgentID {
			agentDef = &cfg.Blueprint.Agents[i]
			break
		}
	}
	if agentDef == nil {
		return nil, fmt.Errorf("agent %q not found in blueprint", cfg.AgentID)
	}

	// Override prompt if specified
	if cfg.Prompt != "" {
		agentDef.Prompt = cfg.Prompt
	}

	// Create and start the floor (furniture, API server, etc.)
	floor := NewFloor(cfg.Blueprint)
	if err := floor.Start(func(string) {}); err != nil {
		return nil, fmt.Errorf("start floor: %w", err)
	}
	defer floor.Stop()

	sess := floor.DefaultSession()

	// Post user input
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: cfg.Input})

	// Build and run the agent synchronously
	var agent Agent
	switch agentDef.Type {
	case "acp":
		agent = NewACPAgent(agentDef)
	default:
		agent = NewLLMAgent(agentDef)
	}

	turn := NewAgentTurn(sess, sess.MainRoom, sess.Floor, cfg.AgentID)
	if err := agent.Run(context.Background(), turn); err != nil {
		return nil, fmt.Errorf("agent %s: %w", cfg.AgentID, err)
	}

	// Find the agent's response in chat history
	history := sess.MainRoom.History()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].From == cfg.AgentID {
			return &RunOnceResult{
				Content:          history[i].Content,
				ToolInteractions: history[i].ToolInteractions,
			}, nil
		}
	}

	// Agent may have passed
	return &RunOnceResult{Content: ""}, nil
}
