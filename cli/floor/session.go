package floor

import (
	"fmt"
)

// MainRoomID is the ID of the default room auto-created in every session.
// Any session agent participates by default; turn-taking uses the session's
// main Controller (not a per-room one). #main never closes.
const MainRoomID = "#main"

// Session is one conversation thread on a Floor. It owns the rooms
// (including the always-present "#main"), per-agent context references,
// and which room each agent is currently in. The actual message log
// lives in Floor.Store; Session is the runtime structure that organizes
// who can see what.
//
// In v1 each Floor has exactly one Session ("default"). The struct is
// designed so multi-session support is "just add another entry to
// Floor.Sessions" — no API changes needed.
type Session struct {
	ID    string
	Floor *Floor // back-reference to shared state (store, furniture, sandbox, APIServer)

	// MainRoom is a convenience pointer to Rooms[MainRoomID]; always non-nil
	// for a Session created via NewSession.
	MainRoom *Room

	Rooms         map[string]*Room
	AgentContexts map[string]*AgentContext

	agentRoom map[string]string // agentID → roomID ("" or MainRoomID = on main)
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
	main.setSession(s)
	for _, a := range floor.Agents {
		s.AddAgentContext(a.ID)
	}
	return s
}

// AddAgentContext creates a new AgentContext for the given agent in this
// session. Visibility for future Post calls is determined at Post time
// from the session's room-membership map, so this method does NOT
// register any listener — it just allocates the context.
func (s *Session) AddAgentContext(agentID string) *AgentContext {
	ac := NewAgentContext(agentID, s)
	s.AgentContexts[agentID] = ac
	return ac
}

// RemoveAgentContext deletes the agent's context and clears membership.
// The agent's event history in the store is preserved (audit); future
// Post calls will not target this agent (it's no longer in any room).
func (s *Session) RemoveAgentContext(agentID string) {
	delete(s.AgentContexts, agentID)
	delete(s.agentRoom, agentID)
}

// GetAgentContext returns the context for the given agent, or nil if not found.
func (s *Session) GetAgentContext(agentID string) *AgentContext {
	return s.AgentContexts[agentID]
}

// AgentRoom returns the room ID an agent is in. Empty string or
// MainRoomID both mean "on main".
func (s *Session) AgentRoom(agentID string) string {
	return s.agentRoom[agentID]
}

// AgentsInRoom returns the agent IDs that currently participate in the
// given room. Used by Room.Post to compute visibility for the store.
// For #main: agents whose agentRoom entry is empty or MainRoomID.
// For sub-rooms: agents whose agentRoom entry equals the room ID.
func (s *Session) AgentsInRoom(roomID string) []string {
	var out []string
	for aid := range s.AgentContexts {
		in := s.agentRoom[aid]
		switch {
		case roomID == MainRoomID:
			if in == "" || in == MainRoomID {
				out = append(out, aid)
			}
		default:
			if in == roomID {
				out = append(out, aid)
			}
		}
	}
	return out
}

// CreateRoom creates an isolated sub-conversation room and moves the
// specified agents into it. Their AgentContext's visibility shifts to
// the new room (future posts to #main no longer reach them; posts to
// the new room do). Inserts a private system message into each agent's
// view about the transition.
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
		if existing := s.agentRoom[aid]; existing != "" && existing != MainRoomID {
			return nil, fmt.Errorf("agent %s is already in room %s", aid, existing)
		}
	}

	room := NewSubRoom(roomID, creator, agentIDs, prompt, s)
	s.Rooms[roomID] = room

	var participantNames []string
	for _, aid := range agentIDs {
		participantNames = append(participantNames, aid)
	}

	// Move agents into the new room first so AppendSystem records the
	// system message against the new room.
	for _, aid := range agentIDs {
		s.agentRoom[aid] = roomID
		ac := s.AgentContexts[aid]
		ac.AppendSystem(fmt.Sprintf("You moved to room %s with %s. Messages here are private to this room.",
			roomID, joinAgentIDs(participantNames, aid)))
	}

	if s.unified != nil {
		go s.forwardRoomEvents(room)
	}

	s.Floor.debug("created room %s with agents %v", roomID, agentIDs)
	return room, nil
}

// CloseRoom closes a sub-room and moves its agents back to the main
// room. Posts a summary to #main. Cannot be used on MainRoomID.
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

	// Move agents back to main BEFORE the system message so it records
	// against #main (not the closing sub-room).
	for aid := range room.AgentIDs {
		delete(s.agentRoom, aid)
		ac := s.AgentContexts[aid]
		ac.AppendSystem(fmt.Sprintf("Room %s closed. You are back on the main floor.", roomID))
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
// PostXxx() calls go to that room instead of the session's #main.
// Shares all maps and the Floor with the parent.
func (s *Session) ForRoom(room *Room) *Session {
	return &Session{
		ID:            s.ID,
		Floor:         s.Floor,
		MainRoom:      room,
		Rooms:         s.Rooms,
		AgentContexts: s.AgentContexts,
		agentRoom:     s.agentRoom,
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
func (s *Session) Close() { s.MainRoom.Close() }
