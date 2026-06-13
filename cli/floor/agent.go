package floor

import "context"

// Agent is a chat participant that can be triggered to run.
// It reads conversation history, does work (LLM calls, ACP protocol),
// and posts results back via its turn handle. The main loop calls
// Run() in a goroutine.
type Agent interface {
	// AgentID returns the agent's ID (e.g. "@data").
	AgentID() string

	// Run executes one turn. The turn handle exposes the small set of
	// engine capabilities the agent needs — reading accumulated context,
	// emitting stream/status events, posting the final reply, and
	// reaching scoped furniture / sandbox / ACP subprocess.
	//
	// Use ctx for cancellation.
	Run(ctx context.Context, turn AgentTurn) error
}
