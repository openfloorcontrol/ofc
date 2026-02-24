package floor

import "context"

// Agent is a chat participant that can be triggered to run.
// It reads chat history, does work (LLM calls, ACP protocol), and posts
// results back to the Chat. The main loop calls Run() in a goroutine.
type Agent interface {
	// AgentID returns the agent's ID (e.g. "@data").
	AgentID() string

	// Run executes one turn. It reads from floor.Chat.History(),
	// posts streaming events via floor.Chat.PostStream(), and posts
	// the final message via floor.Chat.Post(). When done, it returns.
	// Use ctx for cancellation (e.g. user presses ESC).
	Run(ctx context.Context, floor *Floor) error
}
