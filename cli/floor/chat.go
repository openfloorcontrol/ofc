package floor

import "sync"

// ChatMessage is a floor-level message from any participant.
type ChatMessage struct {
	From             string            // "@user", "@data", "@code"
	Content          string            // The text content
	ToolInteractions []ToolInteraction // Tool calls made during this turn
}

// ChatEvent is anything that happens on the chat — messages, streaming, lifecycle.
type ChatEvent interface {
	chatEventMarker()
}

// MessagePosted is emitted when a complete message is added to chat history.
type MessagePosted struct {
	Message ChatMessage
}

// StreamEvent wraps a streaming event (tokens, tool calls) from an agent.
// These are NOT stored in chat history — they're ephemeral display events.
type StreamEvent struct {
	Event Event // TokenStreamed, ToolCallStarted, ToolCallResult, AgentLabel, AgentThinking
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

// Chat is the communication fabric of the floor.
// Thread-safe message history + event channel.
type Chat struct {
	mu       sync.RWMutex
	messages []ChatMessage
	eventCh  chan ChatEvent
}

// NewChat creates a new Chat with a buffered event channel.
func NewChat() *Chat {
	return &Chat{
		eventCh: make(chan ChatEvent, 64),
	}
}

// Post adds a complete message to the history and emits MessagePosted.
func (c *Chat) Post(msg ChatMessage) {
	c.mu.Lock()
	c.messages = append(c.messages, msg)
	c.mu.Unlock()
	c.eventCh <- MessagePosted{Message: msg}
}

// PostStream emits a streaming event without adding to history.
// Used for tokens, tool call progress, thinking indicators.
func (c *Chat) PostStream(ev Event) {
	c.eventCh <- StreamEvent{Event: ev}
}

// PostEvent emits a chat event directly (AgentFinished, AgentPassed, etc.).
func (c *Chat) PostEvent(ev ChatEvent) {
	c.eventCh <- ev
}

// History returns a snapshot of all messages. Safe for concurrent use.
func (c *Chat) History() []ChatMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ChatMessage, len(c.messages))
	copy(result, c.messages)
	return result
}

// Clear removes all messages.
func (c *Chat) Clear() {
	c.mu.Lock()
	c.messages = nil
	c.mu.Unlock()
}

// Events returns the channel for reading chat events.
func (c *Chat) Events() <-chan ChatEvent {
	return c.eventCh
}

// Close closes the event channel. Call when the floor is shutting down.
func (c *Chat) Close() {
	close(c.eventCh)
}
