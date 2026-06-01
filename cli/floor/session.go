package floor

import (
	"fmt"
)

// MainRoomID is the ID of the default room auto-created in every session.
// It functions like the "main chat" did before Chat/Room unification —
// any agent in the session participates by default; turn-taking uses the
// session's main Controller (not a per-room one).
const MainRoomID = "#main"

// Session is one conversation thread on a Floor. It owns the rooms
// (including the always-present "#main"), per-agent memory, and which
// room each agent is currently in. Multiple sessions share their Floor's
// furniture, sandbox, and other shared infrastructure via the Floor
// back-reference.
//
// In v1 each Floor has exactly one Session ("default"). The struct is
// designed so multi-session support is "just add another entry to
// Floor.Sessions" — no API changes needed.
type Session struct {
	ID    string
	Floor *Floor // back-reference to shared state (furniture, sandbox, APIServer)

	// MainRoom is a convenience pointer to Rooms[MainRoomID]. It is always
	// non-nil for a Session created via NewSession.
	MainRoom *Room

	Rooms         map[string]*Room
	AgentContexts map[string]*AgentContext

	agentRoom map[string]string // agentID → roomID (MainRoomID = on main)
	unified   chan TaggedEvent  // lazy, set by StartUnified
}

// NewSession creates a session attached to the given Floor with its
// default "#main" room. AgentContexts are seeded for every agent
// currently on the Floor; subsequent Floor mutations (AddAgent /
// RemoveAgent) propagate to this session too.
func NewSession(id string, floor *Floor) *Session {
	main := NewRoom(MainRoomID)
	s := &Session{
		ID:            id,
		Floor:         floor,
		MainRoom:      main,
		Rooms:         map[string]*Room{MainRoomID: main},
		AgentContexts: make(map[string]*AgentContext),
		agentRoom:     make(map[string]string),
	}
	// Seed contexts for agents already on the floor. (When this is
	// called from NewFloor before any agents are added, the loop is a
	// no-op; the LLM agents added immediately after will propagate to
	// this session via AddAgent.)
	for _, a := range floor.Agents {
		s.AddAgentContext(a.ID)
	}
	return s
}

// AddAgentContext creates a new AgentContext for the given agent in this
// session and registers it as a listener on the main room. Called by
// Floor.AddAgent for each existing session on the floor.
func (s *Session) AddAgentContext(agentID string) *AgentContext {
	ac := NewAgentContext(agentID)
	s.AgentContexts[agentID] = ac
	s.MainRoom.AddListener(ac)
	return ac
}

// RemoveAgentContext unregisters and removes the agent's context.
// Called by Floor.RemoveAgent for each existing session on the floor.
// Note: the context is dropped but its accumulated entries are not
// "preserved for audit" — when persistence lands, audit lives in the
// session's event log, not in dropped AgentContexts.
func (s *Session) RemoveAgentContext(agentID string) {
	ac, ok := s.AgentContexts[agentID]
	if !ok {
		return
	}
	s.MainRoom.RemoveListener(ac)
	delete(s.AgentContexts, agentID)

	// Also unbind from any sub-room the agent was in.
	if roomID := s.agentRoom[agentID]; roomID != "" && roomID != MainRoomID {
		if room, ok := s.Rooms[roomID]; ok {
			room.RemoveListener(ac)
			delete(room.AgentIDs, agentID)
		}
		delete(s.agentRoom, agentID)
	}
}

// GetAgentContext returns the context for the given agent, or nil if not found.
func (s *Session) GetAgentContext(agentID string) *AgentContext {
	return s.AgentContexts[agentID]
}

// AgentRoom returns the room ID an agent is in. The empty string means
// the agent is on the main room (its listener attached to MainRoom).
// Sub-room agents return their sub-room's ID.
func (s *Session) AgentRoom(agentID string) string {
	return s.agentRoom[agentID]
}

// CreateRoom creates an isolated sub-conversation room and moves the
// specified agents into it: their AgentContext switches from listening
// on the main room to listening on the new sub-room. Inserts a system
// message into each agent's context about the transition.
func (s *Session) CreateRoom(roomID, creator string, agentIDs []string, prompt string) (*Room, error) {
	if roomID == MainRoomID {
		return nil, fmt.Errorf("room %s is reserved", MainRoomID)
	}
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

	room := NewSubRoom(roomID, creator, agentIDs, prompt, s.Floor)
	s.Rooms[roomID] = room

	var participantNames []string
	for _, aid := range agentIDs {
		participantNames = append(participantNames, aid)
	}

	for _, aid := range agentIDs {
		ac := s.AgentContexts[aid]
		s.MainRoom.RemoveListener(ac)
		room.AddListener(ac)
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

// CloseRoom closes a sub-room and moves its agents back to the main
// room. Posts a summary to the main room. Cannot be used on MainRoomID.
func (s *Session) CloseRoom(roomID string) error {
	if roomID == MainRoomID {
		return fmt.Errorf("cannot close %s", MainRoomID)
	}
	room, ok := s.Rooms[roomID]
	if !ok {
		return fmt.Errorf("room %s not found", roomID)
	}
	if room.IsClosed() {
		return fmt.Errorf("room %s is already closed", roomID)
	}

	history := room.History()
	var summary string
	if len(history) > 0 {
		last := history[len(history)-1]
		summary = fmt.Sprintf("[Room %s closed] Last message from %s: %s", roomID, last.From, last.Content)
	} else {
		summary = fmt.Sprintf("[Room %s closed] No messages were exchanged.", roomID)
	}

	for aid := range room.AgentIDs {
		ac := s.AgentContexts[aid]
		room.RemoveListener(ac)
		s.MainRoom.AddListener(ac)
		ac.AppendSystem(fmt.Sprintf("Room %s closed. You are back on the main floor.", roomID))
		delete(s.agentRoom, aid)
	}

	room.CloseWithSummary(summary)

	s.MainRoom.Post(ChatMessage{
		From:    "@system",
		Content: summary,
	})

	s.Floor.debug("closed room %s", roomID)
	return nil
}

// ForRoom returns a shallow Session view with MainRoom swapped to the
// given (sub-)room. Agents running in a room receive this view so their
// PostXxx() calls go to that room instead of the session's main room.
// Shares AgentContexts, Rooms, agentRoom, and Floor with the parent.
func (s *Session) ForRoom(room *Room) *Session {
	return &Session{
		ID:            s.ID,
		Floor:         s.Floor,
		MainRoom:      room,
		Rooms:         s.Rooms,
		AgentContexts: s.AgentContexts,
		agentRoom:     s.agentRoom,
		// unified deliberately not propagated — the view doesn't own the merged stream
	}
}

// StartUnified creates a merged event channel that receives events from
// the main room and all sub-rooms. Returns the channel. Sub-room events
// are tagged with their RoomID; main room events have RoomID "".
func (s *Session) StartUnified() <-chan TaggedEvent {
	s.unified = make(chan TaggedEvent, 64)

	go func() {
		for ev := range s.MainRoom.Events() {
			s.unified <- TaggedEvent{RoomID: "", Event: ev}
		}
		close(s.unified)
	}()

	return s.unified
}

// forwardRoomEvents forwards events from a sub-room to the unified
// channel. Terminates when the room's event channel is closed.
func (s *Session) forwardRoomEvents(room *Room) {
	for ev := range room.Events() {
		if s.unified != nil {
			s.unified <- TaggedEvent{RoomID: room.ID, Event: ev}
		}
	}
}

// Close closes the session's main room. Sub-rooms close independently
// via CloseRoom or CloseWithSummary.
func (s *Session) Close() {
	s.MainRoom.Close()
}
