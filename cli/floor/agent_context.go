package floor

// AgentContext is one agent's view of a session's conversation. It is
// a thin reader over the session's store: Entries / Delta query the
// store's per-agent visibility join (ReadForAgent). AppendSystem writes
// a private message visible only to this agent.
//
// AgentContext holds a SessionView (not a *Session) so it depends only
// on the small interface its work actually needs — store access plus
// per-agent room lookup. This keeps it testable without a full Floor.
//
// No in-memory cache: every read goes to the store. For the in-memory
// MemoryStore this is essentially free; for backed stores (JSONL, SQL)
// it's a query per read. If that becomes a hot path we can add a cache
// layer above — the interface won't change.
type AgentContext struct {
	agentID string
	session SessionView
	sentSeq uint64 // for Delta — events with Seq > sentSeq are "new"
}

// NewAgentContext creates a context for the given agent in the given session.
// Typically called by Session.AddAgentContext, not directly.
func NewAgentContext(agentID string, sess SessionView) *AgentContext {
	return &AgentContext{
		agentID: agentID,
		session: sess,
	}
}

// AgentID returns the agent this context belongs to.
func (ac *AgentContext) AgentID() string { return ac.agentID }

// Entries returns every message this agent has seen, in order.
// Used by LLMAgent.buildContext to build the full conversation history.
func (ac *AgentContext) Entries() []*ChatMessage {
	events, _ := ac.session.Store().ReadForAgent(ac.session.ID(), ac.agentID, EventFilter{})
	return extractMessages(events)
}

// Delta returns messages this agent has seen since the last MarkSent.
// Used by ACPAgent to send only incremental updates to the subprocess.
func (ac *AgentContext) Delta() []*ChatMessage {
	events, _ := ac.session.Store().ReadForAgent(ac.session.ID(), ac.agentID, EventFilter{FromSeq: ac.sentSeq})
	return extractMessages(events)
}

// MarkSent records the highest-seen Seq as "sent", so future Delta()
// calls return only newer events.
func (ac *AgentContext) MarkSent() {
	events, _ := ac.session.Store().ReadForAgent(ac.session.ID(), ac.agentID, EventFilter{})
	if len(events) > 0 {
		ac.sentSeq = events[len(events)-1].Seq
	}
}

// AppendSystem inserts a system-level message visible only to this agent.
// Used for room transitions ("You moved to #analysis with @code").
// The message is recorded against the agent's current room.
func (ac *AgentContext) AppendSystem(text string) {
	roomID := ac.session.AgentRoom(ac.agentID)
	if roomID == "" {
		roomID = MainRoomID
	}
	ac.session.Store().Append(AppendOpts{
		SessionID: ac.session.ID(),
		RoomID:    roomID,
		Event:     MessagePostedEvent{Message: ChatMessage{From: "@system", Content: text}},
		VisibleTo: []string{ac.agentID},
		Private:   true, // hidden from Read; only this agent sees it
	})
}

// Len returns the number of messages this agent has seen.
func (ac *AgentContext) Len() int {
	events, _ := ac.session.Store().ReadForAgent(ac.session.ID(), ac.agentID, EventFilter{})
	return len(events)
}

// Clear resets sentSeq to 0. The store's events are not affected — call
// session.MainRoom.Clear() or store.Clear() to actually remove events.
// Used after /clear to make Delta() return everything from the start
// of the (now-empty) log.
func (ac *AgentContext) Clear() {
	ac.sentSeq = 0
}
