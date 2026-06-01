package floor

import "context"

// Agent is a chat participant that can be triggered to run.
// It reads chat history, does work (LLM calls, ACP protocol), and posts
// results back to the session's Chat. The main loop calls Run() in a goroutine.
type Agent interface {
	// AgentID returns the agent's ID (e.g. "@data").
	AgentID() string

	// Run executes one turn within the given session. It reads from
	// sess.Chat.History() (or sess.GetAgentContext(...).Entries()), posts
	// streaming events via sess.Chat.PostStream(), and posts the final
	// message via sess.Chat.Post(). Use ctx for cancellation.
	//
	// Shared resources (furniture, sandbox, ACP subprocesses) are reachable
	// via sess.Floor.
	Run(ctx context.Context, sess *Session) error
}
