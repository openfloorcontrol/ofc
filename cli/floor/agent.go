package floor

import "context"

// Agent is a chat participant that can be triggered to run.
// It reads conversation history, does work (LLM calls, ACP protocol),
// and posts results back to the session's main room. The main loop
// calls Run() in a goroutine.
type Agent interface {
	// AgentID returns the agent's ID (e.g. "@data").
	AgentID() string

	// Run executes one turn within the given session. It reads from
	// sess.MainRoom.History() (or sess.GetAgentContext(...).Entries()),
	// posts streaming events via sess.MainRoom.PostStream(), and posts
	// the final message via sess.MainRoom.Post(). Use ctx for
	// cancellation.
	//
	// Shared resources (furniture, sandbox, ACP subprocesses) are reachable
	// via sess.Floor.
	Run(ctx context.Context, sess *Session) error
}
