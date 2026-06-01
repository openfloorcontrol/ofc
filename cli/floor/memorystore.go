package floor

import (
	"sync"
	"time"
)

// MemoryStore is the in-memory implementation of SessionStore. It models
// the data as two structures inside each session:
//   - events: ordered log of every stored event for the session
//   - agentRefs: per-agent list of sequence numbers (the join table)
//
// All operations are guarded by a single mutex; concurrent Append /
// Read calls serialize. For v1 single-process use, this is fine.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*memSession
}

type memSession struct {
	events    []StoredEvent       // ordered by Seq
	nextSeq   uint64              // assigned on Append (starts at 1)
	agentRefs map[string][]uint64 // agentID → seq numbers in arrival order
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*memSession),
	}
}

func (m *MemoryStore) getSession(sessionID string) *memSession {
	s, ok := m.sessions[sessionID]
	if !ok {
		s = &memSession{
			agentRefs: make(map[string][]uint64),
		}
		m.sessions[sessionID] = s
	}
	return s
}

// appendRaw inserts an event with its Seq and Time pre-assigned. Used
// by backed implementations (e.g. JSONLStore) during load-time replay
// where the stored event already has its metadata. nextSeq is advanced
// past the inserted Seq.
//
// Locking: this is intended for single-threaded replay before the store
// goes live. Callers (load-time only) hold m.mu themselves if needed.
func (m *MemoryStore) appendRaw(sessionID string, ev StoredEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getSession(sessionID)
	s.events = append(s.events, ev)
	if ev.Seq > s.nextSeq {
		s.nextSeq = ev.Seq
	}
}

// addRef records a visibility ref from an agent to an event seq.
// Companion to appendRaw for load-time replay.
func (m *MemoryStore) addRef(sessionID, agentID string, eventSeq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getSession(sessionID)
	s.agentRefs[agentID] = append(s.agentRefs[agentID], eventSeq)
}

// Append implements SessionStore.
func (m *MemoryStore) Append(opts AppendOpts) (StoredEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := m.getSession(opts.SessionID)
	s.nextSeq++

	stored := StoredEvent{
		Seq:     s.nextSeq,
		Time:    time.Now(),
		RoomID:  opts.RoomID,
		Event:   opts.Event,
		Private: opts.Private,
	}
	s.events = append(s.events, stored)
	for _, aid := range opts.VisibleTo {
		s.agentRefs[aid] = append(s.agentRefs[aid], stored.Seq)
	}
	return stored, nil
}

// Read implements SessionStore.
func (m *MemoryStore) Read(sessionID string, filter EventFilter) ([]StoredEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	out := make([]StoredEvent, 0, len(s.events))
	for _, ev := range s.events {
		if ev.Private {
			continue // private events not returned by Read; use ReadForAgent
		}
		if matches(ev, filter) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// ReadForAgent implements SessionStore.
func (m *MemoryStore) ReadForAgent(sessionID, agentID string, filter EventFilter) ([]StoredEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	refs := s.agentRefs[agentID]
	if len(refs) == 0 {
		return nil, nil
	}

	// Build a seq → event index. Can't use seq-1 because Clear leaves
	// gaps in the seq sequence (events removed but seq numbers stay
	// monotonic).
	bySeq := make(map[uint64]StoredEvent, len(s.events))
	for _, ev := range s.events {
		bySeq[ev.Seq] = ev
	}

	out := make([]StoredEvent, 0, len(refs))
	for _, seq := range refs {
		ev, ok := bySeq[seq]
		if !ok {
			continue // event was cleared but ref remained — defensive
		}
		if matches(ev, filter) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Clear implements SessionStore.
func (m *MemoryStore) Clear(sessionID string, filter EventFilter) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}

	// Collect seqs to remove
	removed := make(map[uint64]bool)
	keptEvents := s.events[:0]
	for _, ev := range s.events {
		if matches(ev, filter) {
			removed[ev.Seq] = true
		} else {
			keptEvents = append(keptEvents, ev)
		}
	}
	s.events = keptEvents

	// Prune refs pointing at removed events
	for aid, refs := range s.agentRefs {
		kept := refs[:0]
		for _, seq := range refs {
			if !removed[seq] {
				kept = append(kept, seq)
			}
		}
		s.agentRefs[aid] = kept
	}

	return nil
}

// matches checks whether an event satisfies the filter.
func matches(ev StoredEvent, f EventFilter) bool {
	if f.RoomID != "" && ev.RoomID != f.RoomID {
		return false
	}
	if f.Type != "" && ev.Event.Type() != f.Type {
		return false
	}
	if f.FromSeq != 0 && ev.Seq <= f.FromSeq {
		return false
	}
	return true
}
