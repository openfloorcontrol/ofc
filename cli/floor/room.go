package floor

import (
	"sync"
)

// Room is an isolated sub-conversation on the floor.
// It has its own Chat (for message isolation) and Controller (for turn-taking).
// Agents move into rooms, and their AgentContexts switch to the room's Chat.
// Messages in a room are invisible to agents on the main floor.
type Room struct {
	ID         string
	Chat       *Chat
	Controller *Controller
	Creator    string          // who created it (e.g. "@user", "@data")
	Prompt     string          // initial prompt when room was created
	AgentIDs   map[string]bool // agents currently in this room

	mu      sync.Mutex
	closed  bool
	summary string
}

// NewRoom creates a room with its own Chat and a Controller scoped to
// the given subset of the floor's agents.
func NewRoom(id, creator string, agentIDs []string, prompt string, floor *Floor) *Room {
	room := &Room{
		ID:         id,
		Chat:       NewChat(),
		Controller: NewControllerForRoom(floor, agentIDs),
		Creator:    creator,
		Prompt:     prompt,
		AgentIDs:   make(map[string]bool, len(agentIDs)),
	}
	for _, aid := range agentIDs {
		room.AgentIDs[aid] = true
	}
	return room
}

// Close marks the room as closed and shuts down its Chat.
func (r *Room) Close(summary string) {
	r.mu.Lock()
	r.closed = true
	r.summary = summary
	r.mu.Unlock()
	r.Chat.Close()
}

// IsClosed returns whether the room has been closed.
func (r *Room) IsClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// Summary returns the room's closing summary.
func (r *Room) Summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary
}
