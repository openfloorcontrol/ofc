package frontend

import (
	"sort"

	"github.com/openfloorcontrol/ofc/floor"
)

// SessionClient is a frontend's handle on one session: the session's
// events to render and a way to post input. LocalSession wraps a session
// in this process; RemoteSession talks to an ofc web server over HTTP.
type SessionClient interface {
	// ID is the session's UUID.
	ID() string
	// Info describes the floor, for the header.
	Info() FloorInfo
	// History is the session's #main history up to where Events starts.
	History() []floor.ChatMessage
	// Events delivers the session's events from after History on. It is
	// closed when the session ends or the connection drops.
	Events() <-chan floor.TaggedEvent
	// PostUserInput sends a message or slash command from @user.
	PostUserInput(text string) error
	// Close stops event delivery.
	Close()
}

// FloorInfo describes a floor for frontends.
type FloorInfo struct {
	Name        string
	Description string
	Agents      []string
	Furniture   []string
}

// LocalSession is a SessionClient for a session in this process.
type LocalSession struct {
	floor  *floor.Floor
	sess   *floor.Session
	events <-chan floor.TaggedEvent
}

// NewLocalSession subscribes to sess. Start the session after this, so
// no event is missed.
func NewLocalSession(f *floor.Floor, sess *floor.Session) *LocalSession {
	return &LocalSession{floor: f, sess: sess, events: sess.Subscribe()}
}

func (l *LocalSession) ID() string { return l.sess.ID() }

func (l *LocalSession) Info() FloorInfo {
	info := FloorInfo{Name: l.floor.Blueprint.Name, Description: l.floor.Blueprint.Description}
	for _, a := range l.floor.Blueprint.Agents {
		info.Agents = append(info.Agents, a.ID)
	}
	for name := range l.floor.Furniture {
		info.Furniture = append(info.Furniture, name)
	}
	sort.Strings(info.Furniture)
	return info
}

func (l *LocalSession) History() []floor.ChatMessage { return l.sess.MainRoom.History() }

func (l *LocalSession) Events() <-chan floor.TaggedEvent { return l.events }

func (l *LocalSession) PostUserInput(text string) error {
	l.sess.MainRoom.PostUserInput(text)
	return nil
}

func (l *LocalSession) Close() { l.sess.Unsubscribe(l.events) }
