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
	APIServer APIServer            // optional; one is constructed via the caller's choice (typically api.New())
}

// RunOnceResult holds the agent's response from a single turn, plus the
// stream of events emitted during the run. Callers can walk Events to see
// what the agent produced (thoughts, tool calls, tokens) even when Content
// is empty.
type RunOnceResult struct {
	Content          string
	ToolInteractions []ToolInteraction
	Events           []ChatEvent
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
	if cfg.APIServer == nil {
		return nil, fmt.Errorf("RunOnce: APIServer is nil (pass api.New() in cfg)")
	}
	// Create and start the floor (furniture, API server, etc.)
	f := NewFloor(cfg.Blueprint)
	f.APIServer = cfg.APIServer
	if err := f.Start(func(string) {}); err != nil {
		return nil, fmt.Errorf("start floor: %w", err)
	}
	defer f.Stop()

	sess := f.DefaultSession()

	// Collect events from the room while the agent runs. Room.Post and
	// PostStream send on a buffered channel; a streaming agent fills the
	// buffer in a few dozen tokens and blocks. Without a frontend attached,
	// nothing consumes those events, so we consume them here — and keep
	// them, so diagnostic callers can see what happened.
	events := sess.MainRoom.Events()
	stop := make(chan struct{})
	done := make(chan struct{})
	var collected []ChatEvent
	go func() {
		defer close(done)
		for {
			select {
			case ev := <-events:
				collected = append(collected, ev)
			case <-stop:
				return
			}
		}
	}()

	// Post user input
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: cfg.Input})

	turn := NewAgentTurn(sess, sess.MainRoom, sess.Floor, cfg.AgentID)
	runErr := agent.Run(context.Background(), turn)

	// Stop the collector and pick up any events that landed after it exited.
	// Ownership of `collected` transfers back to this goroutine here.
	close(stop)
	<-done
drain:
	for {
		select {
		case ev := <-events:
			collected = append(collected, ev)
		default:
			break drain
		}
	}

	if runErr != nil {
		return nil, fmt.Errorf("agent %s: %w", cfg.AgentID, runErr)
	}

	// Find the agent's response in chat history
	history := sess.MainRoom.History()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].From == cfg.AgentID {
			return &RunOnceResult{
				Content:          history[i].Content,
				ToolInteractions: history[i].ToolInteractions,
				Events:           collected,
			}, nil
		}
	}

	// Agent may have passed
	return &RunOnceResult{Content: "", Events: collected}, nil
}
