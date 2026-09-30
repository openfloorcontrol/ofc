package floor

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateSession starts a new session on the floor. If SessionMetaTemplate
// is set, it is recorded for the session first.
func (f *Floor) CreateSession() (*Session, error) {
	id := uuid.NewString()
	if f.SessionMetaTemplate != nil {
		meta := *f.SessionMetaTemplate
		meta.CreatedAt = time.Now()
		if err := f.Store.SetMeta(id, meta); err != nil {
			return nil, fmt.Errorf("record session meta: %w", err)
		}
	}

	f.mu.Lock()
	sess := NewSession(id, f)
	f.Sessions[id] = sess
	f.mu.Unlock()

	sess.Start()
	return sess, nil
}

// Session returns the running session with the given ID, resuming it from
// the store if it isn't running yet. Returns ErrSessionNotFound if the
// store holds nothing for it, and an error if its meta names another
// floor's blueprint.
func (f *Floor) Session(id string) (*Session, error) {
	f.mu.Lock()
	sess, ok := f.Sessions[id]
	f.mu.Unlock()
	if !ok {
		if err := f.checkStoredSession(id); err != nil {
			return nil, err
		}
		f.mu.Lock()
		if sess, ok = f.Sessions[id]; !ok {
			sess = NewSession(id, f)
			f.Sessions[id] = sess
		}
		f.mu.Unlock()
	}
	sess.Start()
	return sess, nil
}

// checkStoredSession verifies that the store holds session id and that it
// belongs to this floor.
func (f *Floor) checkStoredSession(id string) error {
	meta, err := f.Store.GetMeta(id)
	switch {
	case err == nil:
		if meta.BlueprintName != f.ID() {
			return fmt.Errorf("session %s belongs to floor %q, not %q", id, meta.BlueprintName, f.ID())
		}
		return nil
	case errors.Is(err, ErrNoSessionMeta):
		events, err := f.Store.Read(id, EventFilter{})
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return ErrSessionNotFound
		}
		return nil
	default:
		return err
	}
}

// ListSessions returns the stored sessions whose meta names this floor's
// blueprint, most recent activity first.
func (f *Floor) ListSessions() ([]SessionInfo, error) {
	all, err := f.Store.List()
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, info := range all {
		if info.Meta != nil && info.Meta.BlueprintName == f.ID() {
			out = append(out, info)
		}
	}
	return out, nil
}

// notifySessions posts a stream event to the main room of every running
// session. Sessions that aren't running are skipped: nothing drains
// their rooms.
func (f *Floor) notifySessions(ev Event) {
	f.mu.Lock()
	var running []*Session
	for _, sess := range f.Sessions {
		if sess.Running() {
			running = append(running, sess)
		}
	}
	f.mu.Unlock()
	for _, sess := range running {
		sess.MainRoom.PostStream(ev)
	}
}
