package floor

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateSession starts a new session on the floor, owned by user ("" for
// nobody in particular). If SessionMetaTemplate is set, it is recorded for
// the session first, with Floor set to this floor and User to user.
func (f *Floor) CreateSession(user string) (*Session, error) {
	id := uuid.NewString()
	if f.SessionMetaTemplate != nil {
		meta := *f.SessionMetaTemplate
		meta.Floor = f.ID()
		meta.CreatedAt = time.Now()
		meta.User = user
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
// floor.
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
		if meta.Floor != f.ID() {
			return fmt.Errorf("session %s belongs to floor %q, not %q", id, meta.Floor, f.ID())
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

// ListSessions returns the stored sessions whose meta names this floor,
// most recent activity first. A non-empty user limits them to
// the ones user may open (see MayOpen).
func (f *Floor) ListSessions(user string) ([]SessionInfo, error) {
	all, err := f.Store.List()
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, info := range all {
		if info.Meta != nil && info.Meta.Floor == f.ID() && mayOpen(info.Meta.User, user) {
			out = append(out, info)
		}
	}
	return out, nil
}

// MayOpen reports whether user may open session id. An empty user (a
// caller nobody identified) may open every session; anyone may open a
// session without an owner. A session the store doesn't hold is
// reported as allowed: opening it fails with ErrSessionNotFound anyway.
func (f *Floor) MayOpen(id, user string) (bool, error) {
	if user == "" {
		return true, nil
	}
	meta, err := f.Store.GetMeta(id)
	if errors.Is(err, ErrNoSessionMeta) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return mayOpen(meta.User, user), nil
}

func mayOpen(owner, user string) bool {
	return user == "" || owner == "" || owner == user
}

// Busy reports whether any of the floor's sessions is busy (Session.Busy):
// someone follows it, or an agent is taking a turn in it.
func (f *Floor) Busy() bool {
	f.mu.Lock()
	sessions := make([]*Session, 0, len(f.Sessions))
	for _, sess := range f.Sessions {
		sessions = append(sessions, sess)
	}
	f.mu.Unlock()
	for _, sess := range sessions {
		if sess.Busy() {
			return true
		}
	}
	return false
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
