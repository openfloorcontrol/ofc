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
	Input     string               // the user input to post
}

// RunOnceResult holds the agent's response from a single turn.
type RunOnceResult struct {
	Content          string
	ToolInteractions []ToolInteraction
}

// RunOnce starts a floor, posts a single user message, runs the given
// agent against it once, and returns the response.
//
// No controller, no multi-agent turn-taking — just: start floor, run
// agent, get response. Used by eval/ for one-shot LLM evaluation.
//
// The caller constructs the agent (typically llm.New or acp.New) so
// this package doesn't have to import the agent subpackages.
func RunOnce(cfg RunOnceConfig, agent Agent) (*RunOnceResult, error) {
	if agent == nil {
		return nil, fmt.Errorf("RunOnce: agent is nil")
	}
	// Create and start the floor (furniture, API server, etc.)
	f := NewFloor(cfg.Blueprint)
	if err := f.Start(func(string) {}); err != nil {
		return nil, fmt.Errorf("start floor: %w", err)
	}
	defer f.Stop()

	sess := f.DefaultSession()

	// Post user input
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: cfg.Input})

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
