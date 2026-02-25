package floor

import (
	"strings"
	"sync"
)

// ChatMessage is a floor-level message from any participant.
type ChatMessage struct {
	From             string            // "@user", "@data", "@code", "@system"
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

// MessageListener receives messages pushed by a Chat.
// Implemented by AgentContext to accumulate per-agent history.
type MessageListener interface {
	OnMessage(msg *ChatMessage)
}

// Chat is the communication fabric of the floor.
// Thread-safe message history + event channel with fan-out to subscribers.
type Chat struct {
	mu       sync.RWMutex
	messages []*ChatMessage
	eventCh  chan ChatEvent

	subMu       sync.Mutex
	subscribers []chan ChatEvent

	listenerMu sync.Mutex
	listeners  []MessageListener
}

// NewChat creates a new Chat with a buffered event channel.
func NewChat() *Chat {
	return &Chat{
		eventCh: make(chan ChatEvent, 64),
	}
}

// Post adds a complete message to the history and emits MessagePosted.
func (c *Chat) Post(msg ChatMessage) {
	m := &msg // heap-allocate for stable pointer
	c.mu.Lock()
	c.messages = append(c.messages, m)
	c.mu.Unlock()

	// Notify message listeners (AgentContexts)
	c.notifyListeners(m)

	ev := MessagePosted{Message: *m}
	c.eventCh <- ev
	c.fanOut(ev)
}

// PostStream emits a streaming event without adding to history.
// Used for tokens, tool call progress, thinking indicators.
func (c *Chat) PostStream(ev Event) {
	se := StreamEvent{Event: ev}
	c.eventCh <- se
	c.fanOut(se)
}

// PostEvent emits a chat event directly (AgentFinished, AgentPassed, etc.).
func (c *Chat) PostEvent(ev ChatEvent) {
	c.eventCh <- ev
	c.fanOut(ev)
}

// fanOut sends an event to all subscribers (non-blocking).
func (c *Chat) fanOut(ev ChatEvent) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, ch := range c.subscribers {
		select {
		case ch <- ev:
		default:
			// Subscriber too slow, drop event
		}
	}
}

// notifyListeners pushes a message to all registered listeners.
func (c *Chat) notifyListeners(msg *ChatMessage) {
	c.listenerMu.Lock()
	defer c.listenerMu.Unlock()
	for _, l := range c.listeners {
		l.OnMessage(msg)
	}
}

// AddListener registers a MessageListener that receives every posted message.
func (c *Chat) AddListener(l MessageListener) {
	c.listenerMu.Lock()
	c.listeners = append(c.listeners, l)
	c.listenerMu.Unlock()
}

// RemoveListener unregisters a MessageListener.
func (c *Chat) RemoveListener(l MessageListener) {
	c.listenerMu.Lock()
	defer c.listenerMu.Unlock()
	for i, existing := range c.listeners {
		if existing == l {
			c.listeners = append(c.listeners[:i], c.listeners[i+1:]...)
			return
		}
	}
}

// Subscribe returns a new channel that receives all chat events.
// Call Unsubscribe when done to clean up.
func (c *Chat) Subscribe() <-chan ChatEvent {
	ch := make(chan ChatEvent, 64)
	c.subMu.Lock()
	c.subscribers = append(c.subscribers, ch)
	c.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (c *Chat) Unsubscribe(ch <-chan ChatEvent) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for i, sub := range c.subscribers {
		if sub == ch {
			c.subscribers = append(c.subscribers[:i], c.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// History returns a snapshot of all messages. Safe for concurrent use.
func (c *Chat) History() []ChatMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ChatMessage, len(c.messages))
	for i, m := range c.messages {
		result[i] = *m
	}
	return result
}

// Clear removes all messages and notifies listeners.
func (c *Chat) Clear() {
	c.mu.Lock()
	c.messages = nil
	c.mu.Unlock()

	// Notify listeners that support clearing
	c.listenerMu.Lock()
	defer c.listenerMu.Unlock()
	type clearer interface{ Clear() }
	for _, l := range c.listeners {
		if cl, ok := l.(clearer); ok {
			cl.Clear()
		}
	}
}

// Events returns the channel for reading chat events.
func (c *Chat) Events() <-chan ChatEvent {
	return c.eventCh
}

// PostUserInput routes user input: slash commands become UserCommandEvents,
// everything else becomes a ChatMessage from @user.
func (c *Chat) PostUserInput(text string) {
	if strings.HasPrefix(text, "/") {
		c.PostEvent(UserCommandEvent{Command: text})
	} else {
		c.Post(ChatMessage{From: "@user", Content: text})
	}
}

// IsCommand returns whether the text is a slash command.
func IsCommand(text string) bool {
	return strings.HasPrefix(text, "/")
}

// Close closes the event channel. Call when the floor is shutting down.
func (c *Chat) Close() {
	close(c.eventCh)
}
