package floor

import (
	"fmt"
)

// Session is one conversation thread on a Floor. It owns the conversation
// log (rooms with messages), per-agent memory, and the room structure.
// Multiple sessions share their Floor's furniture, sandbox, and other
// shared infrastructure via the Floor back-reference.
//
// In v1 each Floor has exactly one Session ("default"). The struct is
// designed so multi-session support is "just add another entry to
// Floor.Sessions" — no API changes needed.
type Session struct {
	ID    string
	Floor *Floor // back-reference to shared state (furniture, sandbox, APIServer)

	Chat          *Chat
	Rooms         map[string]*Room
	AgentContexts map[string]*AgentContext

	agentRoom map[string]string // agentID → roomID ("" = main)
	unified   chan TaggedEvent  // lazy, set by StartUnified
}

// NewSession creates a session attached to the given Floor.
// Iterates the floor's blueprint agents to create AgentContexts and
// registers them as listeners on the session's main Chat.
func NewSession(id string, floor *Floor) *Session {
	chat := NewChat()
	contexts := make(map[string]*AgentContext, len(floor.Blueprint.Agents))
	for _, a := range floor.Blueprint.Agents {
		ctx := NewAgentContext(a.ID)
		contexts[a.ID] = ctx
		chat.AddListener(ctx)
	}
	return &Session{
		ID:            id,
		Floor:         floor,
		Chat:          chat,
		Rooms:         make(map[string]*Room),
		AgentContexts: contexts,
		agentRoom:     make(map[string]string),
	}
}

// GetAgentContext returns the context for the given agent, or nil if not found.
func (s *Session) GetAgentContext(agentID string) *AgentContext {
	return s.AgentContexts[agentID]
}

// AgentRoom returns the room ID an agent is in ("" = main).
func (s *Session) AgentRoom(agentID string) string {
	return s.agentRoom[agentID]
}

// CreateRoom creates an isolated sub-conversation room.
// Moves the specified agents' listeners from the main Chat to the room's Chat,
// and inserts system messages into each agent's context about the transition.
func (s *Session) CreateRoom(roomID, creator string, agentIDs []string, prompt string) (*Room, error) {
	if _, exists := s.Rooms[roomID]; exists {
		return nil, fmt.Errorf("room %s already exists", roomID)
	}
	for _, aid := range agentIDs {
		if _, ok := s.AgentContexts[aid]; !ok {
			return nil, fmt.Errorf("unknown agent %s", aid)
		}
		if existing := s.agentRoom[aid]; existing != "" {
			return nil, fmt.Errorf("agent %s is already in room %s", aid, existing)
		}
	}

	room := NewRoom(roomID, creator, agentIDs, prompt, s.Floor.Blueprint)
	s.Rooms[roomID] = room

	var participantNames []string
	for _, aid := range agentIDs {
		participantNames = append(participantNames, aid)
	}

	for _, aid := range agentIDs {
		ac := s.AgentContexts[aid]
		s.Chat.RemoveListener(ac)
		room.Chat.AddListener(ac)
		ac.AppendSystem(fmt.Sprintf("You moved to room %s with %s. Messages here are private to this room.",
			roomID, joinAgentIDs(participantNames, aid)))
		s.agentRoom[aid] = roomID
	}

	if s.unified != nil {
		go s.forwardRoomEvents(room)
	}

	s.Floor.debug("created room %s with agents %v", roomID, agentIDs)
	return room, nil
}

// CloseRoom closes a room and moves agents back to the main session chat.
// Posts a summary to the main chat.
func (s *Session) CloseRoom(roomID string) error {
	room, ok := s.Rooms[roomID]
	if !ok {
		return fmt.Errorf("room %s not found", roomID)
	}
	if room.IsClosed() {
		return fmt.Errorf("room %s is already closed", roomID)
	}

	history := room.Chat.History()
	var summary string
	if len(history) > 0 {
		last := history[len(history)-1]
		summary = fmt.Sprintf("[Room %s closed] Last message from %s: %s", roomID, last.From, last.Content)
	} else {
		summary = fmt.Sprintf("[Room %s closed] No messages were exchanged.", roomID)
	}

	for aid := range room.AgentIDs {
		ac := s.AgentContexts[aid]
		room.Chat.RemoveListener(ac)
		s.Chat.AddListener(ac)
		ac.AppendSystem(fmt.Sprintf("Room %s closed. You are back on the main floor.", roomID))
		delete(s.agentRoom, aid)
	}

	room.Close(summary)

	s.Chat.Post(ChatMessage{
		From:    "@system",
		Content: summary,
	})

	s.Floor.debug("closed room %s", roomID)
	return nil
}

// ForRoom returns a shallow Session view with Chat swapped to the room's Chat.
// Agents running in a room receive this view so their Chat.Post() goes to
// the room. Shares AgentContexts, Rooms, agentRoom, and Floor with the parent.
func (s *Session) ForRoom(room *Room) *Session {
	return &Session{
		ID:            s.ID,
		Floor:         s.Floor,
		Chat:          room.Chat,
		Rooms:         s.Rooms,
		AgentContexts: s.AgentContexts,
		agentRoom:     s.agentRoom,
		// unified deliberately not propagated — the view doesn't own the merged stream
	}
}

// StartUnified creates a merged event channel that receives events from
// the main chat and all active rooms. Returns the channel.
// Room events are tagged with their RoomID; main chat events have RoomID "".
func (s *Session) StartUnified() <-chan TaggedEvent {
	s.unified = make(chan TaggedEvent, 64)

	go func() {
		for ev := range s.Chat.Events() {
			s.unified <- TaggedEvent{RoomID: "", Event: ev}
		}
		close(s.unified)
	}()

	return s.unified
}

// forwardRoomEvents forwards events from a room's Chat to the unified channel.
// Terminates when the room's Chat is closed.
func (s *Session) forwardRoomEvents(room *Room) {
	for ev := range room.Chat.Events() {
		if s.unified != nil {
			s.unified <- TaggedEvent{RoomID: room.ID, Event: ev}
		}
	}
}

// Close closes the session's main chat.
func (s *Session) Close() {
	s.Chat.Close()
}
