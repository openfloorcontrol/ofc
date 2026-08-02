package floor

import "strings"

// ChatMessage is a session-level message from any participant.
type ChatMessage struct {
	From             string            // "@user", "@data", "@code", "@system"
	Content          string            // The text content
	Reasoning        string            // Model reasoning, if any — displayed but never fed back as context
	ToolInteractions []ToolInteraction // Tool calls made during this turn
}

// ChatEvent is anything that happens in a room — messages, streaming, lifecycle.
type ChatEvent interface {
	chatEventMarker()
}

// MessagePosted is emitted when a complete message is added to history.
type MessagePosted struct {
	Message ChatMessage
}

// StreamEvent wraps a streaming event (tokens, tool calls) from an agent.
// These are NOT stored in history — they're ephemeral display events.
type StreamEvent struct {
	Event Event // TokenStreamed, ThoughtStreamed, ToolCallStarted, ToolCallResult, AgentLabel
}

// AgentFinished is emitted when an agent's Run() completes (with a message).
type AgentFinished struct {
	AgentID string
}

// AgentPassedEvent is emitted when an agent responds with [PASS].
type AgentPassedEvent struct {
	AgentID string
}

// AgentErrorEvent is emitted when an agent encounters an error.
type AgentErrorEvent struct {
	AgentID string
	Err     error
	Partial string
}

// UserCommandEvent is emitted when the user types a slash command.
type UserCommandEvent struct {
	Command string
}

func (MessagePosted) chatEventMarker()    {}
func (StreamEvent) chatEventMarker()      {}
func (AgentFinished) chatEventMarker()    {}
func (AgentPassedEvent) chatEventMarker() {}
func (AgentErrorEvent) chatEventMarker()  {}
func (UserCommandEvent) chatEventMarker() {}

// IsCommand returns whether the text is a slash command.
func IsCommand(text string) bool {
	return strings.HasPrefix(text, "/")
}
