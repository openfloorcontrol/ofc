package floor

import (
	"strings"
	"sync"
)

// Room is a conversation space within a Session. Every session has at
// least one room — "#main" — auto-created by NewSession. Sub-rooms can
// be spawned for isolated conversations (e.g. "#analysis") with their
// own membership and turn-taking.
//
// A Room owns its own message log, event channel, and listener/subscriber
// distribution. Sub-rooms additionally carry:
//   - AgentIDs: which agents are currently in the room
//   - Controller: turn-taking scoped to the room's members
//   - Creator, Prompt: context about why the room was created
//   - closed / summary: lifecycle state (sub-rooms can close; #main does not)
//
// For "#main", the sub-room fields are zero values: no controller (the
// session's main controller lives elsewhere), no membership filter (all
// session agents participate by default), no Creator/Prompt, never closes.
type Room struct {
	ID string

	// Message log + event distribution (was floor.Chat).
	mu         sync.RWMutex
	messages   []*ChatMessage
	eventCh    chan ChatEvent
	subMu      sync.Mutex
	subscribers []chan ChatEvent
	listenerMu sync.Mutex
	listeners  []MessageListener

	// Sub-room state. For #main these are zero-valued.
	Creator    string
	Prompt     string
	AgentIDs   map[string]bool
	Controller *Controller

	closeMu sync.Mutex
	closed  bool
	summary string
}

// NewRoom creates a basic room with the given ID. Used for #main and as
// the base for sub-rooms.
func NewRoom(id string) *Room {
	return &Room{
		ID:      id,
		eventCh: make(chan ChatEvent, 64),
	}
}

// NewSubRoom creates a sub-room: a Room with membership (AgentIDs), a
// scoped Controller, and creation context (Creator, Prompt). Used by
// Session.CreateRoom for spawning isolated sub-conversations.
func NewSubRoom(id, creator string, agentIDs []string, prompt string, floor *Floor) *Room {
	r := NewRoom(id)
	r.Creator = creator
	r.Prompt = prompt
	r.AgentIDs = make(map[string]bool, len(agentIDs))
	for _, aid := range agentIDs {
		r.AgentIDs[aid] = true
	}
	r.Controller = NewControllerForRoom(floor, agentIDs)
	return r
}

// --- Message log methods (formerly on Chat) ---

// Post adds a complete message to the history and emits MessagePosted.
func (r *Room) Post(msg ChatMessage) {
	m := &msg // heap-allocate for stable pointer
	r.mu.Lock()
	r.messages = append(r.messages, m)
	r.mu.Unlock()

	r.notifyListeners(m)

	ev := MessagePosted{Message: *m}
	r.eventCh <- ev
	r.fanOut(ev)
}

// PostStream emits a streaming event without adding to history.
// Used for tokens, tool call progress, thinking indicators.
func (r *Room) PostStream(ev Event) {
	se := StreamEvent{Event: ev}
	r.eventCh <- se
	r.fanOut(se)
}

// PostEvent emits a chat event directly (AgentFinished, AgentPassed, etc.).
func (r *Room) PostEvent(ev ChatEvent) {
	r.eventCh <- ev
	r.fanOut(ev)
}

// PostUserInput routes user input: slash commands become UserCommandEvents,
// everything else becomes a ChatMessage from @user.
func (r *Room) PostUserInput(text string) {
	if strings.HasPrefix(text, "/") {
		r.PostEvent(UserCommandEvent{Command: text})
	} else {
		r.Post(ChatMessage{From: "@user", Content: text})
	}
}

// History returns a snapshot of all messages. Safe for concurrent use.
func (r *Room) History() []ChatMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ChatMessage, len(r.messages))
	for i, m := range r.messages {
		result[i] = *m
	}
	return result
}

// Events returns the channel for reading chat events.
func (r *Room) Events() <-chan ChatEvent {
	return r.eventCh
}

// Subscribe returns a new channel that receives all chat events.
// Call Unsubscribe when done to clean up.
func (r *Room) Subscribe() <-chan ChatEvent {
	ch := make(chan ChatEvent, 64)
	r.subMu.Lock()
	r.subscribers = append(r.subscribers, ch)
	r.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (r *Room) Unsubscribe(ch <-chan ChatEvent) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	for i, sub := range r.subscribers {
		if sub == ch {
			r.subscribers = append(r.subscribers[:i], r.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// AddListener registers a MessageListener that receives every posted message.
func (r *Room) AddListener(l MessageListener) {
	r.listenerMu.Lock()
	r.listeners = append(r.listeners, l)
	r.listenerMu.Unlock()
}

// RemoveListener unregisters a MessageListener.
func (r *Room) RemoveListener(l MessageListener) {
	r.listenerMu.Lock()
	defer r.listenerMu.Unlock()
	for i, existing := range r.listeners {
		if existing == l {
			r.listeners = append(r.listeners[:i], r.listeners[i+1:]...)
			return
		}
	}
}

// Clear removes all messages and notifies listeners that support clearing.
func (r *Room) Clear() {
	r.mu.Lock()
	r.messages = nil
	r.mu.Unlock()

	r.listenerMu.Lock()
	defer r.listenerMu.Unlock()
	type clearer interface{ Clear() }
	for _, l := range r.listeners {
		if cl, ok := l.(clearer); ok {
			cl.Clear()
		}
	}
}

// Close closes the event channel. Call when shutting down.
func (r *Room) Close() {
	close(r.eventCh)
}

// fanOut sends an event to all subscribers (non-blocking).
func (r *Room) fanOut(ev ChatEvent) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	for _, ch := range r.subscribers {
		select {
		case ch <- ev:
		default:
			// Subscriber too slow, drop event
		}
	}
}

// notifyListeners pushes a message to all registered listeners.
func (r *Room) notifyListeners(msg *ChatMessage) {
	r.listenerMu.Lock()
	defer r.listenerMu.Unlock()
	for _, l := range r.listeners {
		l.OnMessage(msg)
	}
}

// --- Sub-room lifecycle (was Room) ---

// CloseWithSummary marks the room as closed with a summary and shuts down
// its event channel. Used by Session.CloseRoom for sub-rooms; #main is
// not expected to use this path.
func (r *Room) CloseWithSummary(summary string) {
	r.closeMu.Lock()
	r.closed = true
	r.summary = summary
	r.closeMu.Unlock()
	r.Close()
}

// IsClosed returns whether the room has been marked closed via CloseWithSummary.
func (r *Room) IsClosed() bool {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.closed
}

// Summary returns the room's closing summary (set by CloseWithSummary).
func (r *Room) Summary() string {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.summary
}
