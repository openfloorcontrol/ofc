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
// Storage is reached through the SessionView; Room.Post writes there.
// Room itself holds only runtime fan-out state (event channel,
// subscribers) and sub-room metadata (membership, Controller).
//
// Room holds a SessionView (not a *Session) so it depends only on the
// small interface its work actually needs — store access plus room
// membership lookup. Standalone rooms used as test fixtures pass nil
// and skip the storage path.
//
// For "#main" the sub-room fields are zero values: no controller (the
// session's main controller lives elsewhere), no AgentIDs filter (all
// session agents are members unless they've moved to a sub-room), no
// Creator/Prompt, never closes.
type Room struct {
	ID      string
	session SessionView // nil for standalone rooms (tests)

	// Runtime event distribution (NOT persistence).
	eventCh     chan ChatEvent
	subMu       sync.Mutex
	subscribers []chan ChatEvent

	// Sub-room state. For #main these are zero-valued.
	Creator    string
	Prompt     string
	AgentIDs   map[string]bool
	Controller *Controller

	closeMu sync.Mutex
	closed  bool
	summary string
}

// NewRoom creates a basic room with the given ID. Used for #main and
// for stand-alone test rooms (which won't have a session back-ref and
// therefore skip storage on Post — only the runtime fan-out fires).
func NewRoom(id string) *Room {
	return &Room{
		ID:      id,
		eventCh: make(chan ChatEvent, 64),
	}
}

// NewSubRoom creates a sub-room: a Room with membership (AgentIDs), a
// scoped Controller, and creation context (Creator, Prompt). Used by
// Session.CreateRoom for spawning isolated sub-conversations.
//
// reg is the agent registry the sub-room's controller will consult —
// typically the Floor.
func NewSubRoom(id, creator string, agentIDs []string, prompt string, sess SessionView, reg AgentRegistry) *Room {
	r := NewRoom(id)
	r.session = sess
	r.Creator = creator
	r.Prompt = prompt
	r.AgentIDs = make(map[string]bool, len(agentIDs))
	for _, aid := range agentIDs {
		r.AgentIDs[aid] = true
	}
	r.Controller = NewControllerForRoom(reg, agentIDs)
	return r
}

// setSession is used internally by NewSession to inject the view for
// #main (which is created before the Session is fully constructed).
func (r *Room) setSession(s SessionView) { r.session = s }

// --- Message log methods ---

// Post writes a message to the session's store (visible to whichever
// agents are currently in this room) and fires runtime events for
// frontends.
func (r *Room) Post(msg ChatMessage) {
	r.appendToStore(msg)

	ev := MessagePosted{Message: msg}
	r.eventCh <- ev
	r.fanOut(ev)
}

// appendToStore writes the message to the session store with VisibleTo
// computed from current room membership. No-op if r.session is nil
// (stand-alone room — typically a test fixture).
func (r *Room) appendToStore(msg ChatMessage) {
	if r.session == nil {
		return
	}
	visibleTo := r.session.AgentsInRoom(r.ID)
	r.session.Store().Append(AppendOpts{
		SessionID: r.session.ID(),
		RoomID:    r.ID,
		Event:     MessagePostedEvent{Message: msg},
		VisibleTo: visibleTo,
	})
}

// PostStream emits a streaming event without storing it. Used for
// tokens, tool call progress, thinking indicators.
func (r *Room) PostStream(ev Event) {
	se := StreamEvent{Event: ev}
	r.eventCh <- se
	r.fanOut(se)
}

// PostEvent emits a chat event directly (AgentFinished, AgentPassed, etc.).
// Not stored — these are runtime decisions, not conversation history.
func (r *Room) PostEvent(ev ChatEvent) {
	r.eventCh <- ev
	r.fanOut(ev)
}

// PostUserInput routes user input: slash commands become UserCommandEvents,
// everything else becomes a ChatMessage from @user (which goes through Post
// and is therefore stored).
func (r *Room) PostUserInput(text string) {
	if strings.HasPrefix(text, "/") {
		r.PostEvent(UserCommandEvent{Command: text})
	} else {
		r.Post(ChatMessage{From: "@user", Content: text})
	}
}

// History returns a snapshot of all messages in this room.
// Sourced from the session store.
func (r *Room) History() []ChatMessage {
	if r.session == nil {
		return nil
	}
	events, _ := r.session.Store().Read(r.session.ID(), EventFilter{RoomID: r.ID})
	out := make([]ChatMessage, 0, len(events))
	for _, ev := range events {
		if mp, ok := ev.Event.(MessagePostedEvent); ok {
			out = append(out, mp.Message)
		}
	}
	return out
}

// Events returns the channel for reading chat events.
func (r *Room) Events() <-chan ChatEvent { return r.eventCh }

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

// Clear removes all messages in this room from the session store.
// Has no effect on the runtime event channel.
func (r *Room) Clear() {
	if r.session == nil {
		return
	}
	r.session.Store().Clear(r.session.ID(), EventFilter{RoomID: r.ID})
}

// Close closes the event channel. Call when shutting down.
func (r *Room) Close() { close(r.eventCh) }

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

// --- Sub-room lifecycle ---

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
