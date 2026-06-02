package floor

import (
	"errors"
	"time"
)

// SessionEvent is the payload of a stored event. Concrete types today:
//   - MessagePostedEvent
//
// Future: RoomCreatedEvent, RoomClosedEvent, CompactionEvent,
// AgentMemoryEntry, etc. Adding a type means adding a struct that
// implements SessionEvent — storage backends serialize via Type() for
// JSON/SQL/etc. without further changes.
type SessionEvent interface {
	Type() string
}

// MessagePostedEvent records a chat message. In v1 this is the only
// concrete event type.
type MessagePostedEvent struct {
	Message ChatMessage
}

// Type implements SessionEvent.
func (MessagePostedEvent) Type() string { return "message_posted" }

// StoredEvent wraps a SessionEvent with metadata assigned by the store.
// This is what Read / ReadForAgent return.
type StoredEvent struct {
	Seq     uint64       // monotonic per session, starts at 1
	Time    time.Time    // server-side timestamp
	RoomID  string       // the room the event happened in; "" for session-scoped
	Event   SessionEvent // the payload
	Private bool         // if true, only ReadForAgent returns this; Read (display) skips it
}

// EventFilter narrows reads. Zero value matches every event.
type EventFilter struct {
	RoomID  string // match events with this RoomID
	Type    string // match SessionEvent.Type()
	FromSeq uint64 // only events with Seq > FromSeq
}

// SessionMeta records the context a session was started in, so we can
// flag when a resume looks inconsistent (different cwd, blueprint
// changed, etc.). In a SQL backend this would map to its own table;
// in JSONL it lives as a "meta" record (typically the first line).
//
// The sessionID is the store key, not a field on the meta itself —
// SetMeta and GetMeta take it as a separate argument.
type SessionMeta struct {
	CWD           string    `json:"cwd"`
	BlueprintPath string    `json:"blueprint_path"`
	BlueprintName string    `json:"blueprint_name"`
	BlueprintHash string    `json:"blueprint_hash"` // sha256 hex of blueprint file contents
	OfcVersion    string    `json:"ofc_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// ErrNoSessionMeta is returned by GetMeta when no metadata has been
// recorded for the session. Common for sessions created before this
// feature shipped, or for in-memory sessions where SetMeta was never
// called.
var ErrNoSessionMeta = errors.New("no session metadata recorded")

// AppendOpts groups Append's parameters. RoomID and VisibleTo are
// optional; Event is required.
type AppendOpts struct {
	SessionID string
	RoomID    string       // "" for session-scoped events
	Event     SessionEvent // the payload
	VisibleTo []string     // agent IDs that should see this event
	Private   bool         // if true, event is per-agent only — never returned by Read
}

// SessionStore persists session events. Backends model the data as
// two logical sets: an ordered event log (Read) and a per-agent
// visibility join (ReadForAgent). The interface is semantic — backends
// choose storage layout freely (one table, two tables, two files, etc.).
//
// Append is atomic across event + visibility refs: a partial Append
// (event written but refs lost) is not allowed.
type SessionStore interface {
	// Append writes a session event and records visibility refs for
	// each agent in opts.VisibleTo. Returns the event with Seq + Time
	// populated.
	Append(opts AppendOpts) (StoredEvent, error)

	// Read returns session events matching the filter, in Seq order.
	// Used by Room.History for display.
	Read(sessionID string, filter EventFilter) ([]StoredEvent, error)

	// ReadForAgent returns events visible to the agent, in Seq order.
	// Conceptually: SELECT events JOIN refs ON event_seq WHERE agent_id=?
	// Used by AgentContext.Entries to construct LLM context.
	ReadForAgent(sessionID, agentID string, filter EventFilter) ([]StoredEvent, error)

	// Clear removes events matching the filter and any visibility refs
	// that point to them.
	Clear(sessionID string, filter EventFilter) error

	// SetMeta records (or replaces) the SessionMeta for a session.
	// Called by the CLI on session creation.
	SetMeta(sessionID string, meta SessionMeta) error

	// GetMeta returns the SessionMeta for the session, or
	// ErrNoSessionMeta if none was recorded.
	GetMeta(sessionID string) (SessionMeta, error)
}

// extractMessages picks ChatMessages out of a stored-event list.
// Convenience for AgentContext.Entries / Room.History callers that
// just want messages (not lifecycle events).
func extractMessages(events []StoredEvent) []*ChatMessage {
	msgs := make([]*ChatMessage, 0, len(events))
	for i := range events {
		if mp, ok := events[i].Event.(MessagePostedEvent); ok {
			m := mp.Message // copy so callers can't mutate stored payload
			msgs = append(msgs, &m)
		}
	}
	return msgs
}
