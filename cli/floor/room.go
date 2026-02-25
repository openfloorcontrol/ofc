package floor

import (
	"sync"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// Room is an isolated sub-conversation on the floor.
// It has its own Chat (for message isolation) and Controller (for turn-taking).
// Agents move into rooms, and their AgentContexts switch to the room's Chat.
// Messages in a room are invisible to agents on the main floor.
type Room struct {
	ID         string
	Chat       *Chat
	Controller *Controller
	Creator    string            // who created it (e.g. "@user", "@data")
	Prompt     string            // initial prompt when room was created
	AgentIDs   map[string]bool   // agents currently in this room

	mu      sync.Mutex
	closed  bool
	summary string
}

// NewRoom creates a room with its own Chat and Controller.
// The Controller uses a filtered blueprint containing only the room's agents.
func NewRoom(id, creator string, agentIDs []string, prompt string, parentBP *blueprint.Blueprint) *Room {
	// Filter blueprint to only include room agents
	filteredBP := &blueprint.Blueprint{
		Name: parentBP.Name + "/" + id,
	}
	for _, agent := range parentBP.Agents {
		for _, aid := range agentIDs {
			if agent.ID == aid {
				filteredBP.Agents = append(filteredBP.Agents, agent)
				break
			}
		}
	}

	room := &Room{
		ID:         id,
		Chat:       NewChat(),
		Controller: NewController(filteredBP),
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
