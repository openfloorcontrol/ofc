package floor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// sessionSubscriber receives every event the session loop handles or emits.
// done is closed by Unsubscribe so a blocked send can give up.
type sessionSubscriber struct {
	ch   chan TaggedEvent
	done chan struct{}
}

// sessionLoop is the turn-taking state owned by a started Session.
type sessionLoop struct {
	startOnce   sync.Once
	started     atomic.Bool
	subMu       sync.Mutex
	subscribers []*sessionSubscriber
	stopped     bool // after /quit: keep forwarding events, dispatch nothing
}

// Subscribe returns a channel of every event in the session: room events
// (tagged with their room) and the loop's own lifecycle events
// (AgentStarted, AwaitingInput, InfoEvent, SessionCleared,
// SessionStopped). Delivery blocks, so a subscriber must keep reading
// until it calls Unsubscribe. The channel is closed when the session
// shuts down.
func (s *Session) Subscribe() <-chan TaggedEvent {
	sub := &sessionSubscriber{
		ch:   make(chan TaggedEvent, 64),
		done: make(chan struct{}),
	}
	s.loop.subMu.Lock()
	s.loop.subscribers = append(s.loop.subscribers, sub)
	s.loop.subMu.Unlock()
	return sub.ch
}

// Unsubscribe stops delivery to ch. Events already buffered in ch stay
// there; ch itself is closed when the session shuts down.
func (s *Session) Unsubscribe(ch <-chan TaggedEvent) {
	s.loop.subMu.Lock()
	defer s.loop.subMu.Unlock()
	for i, sub := range s.loop.subscribers {
		if sub.ch == ch {
			close(sub.done)
			s.loop.subscribers = append(s.loop.subscribers[:i], s.loop.subscribers[i+1:]...)
			return
		}
	}
}

// Start runs the session's turn-taking loop in a goroutine. The loop is
// the only consumer of the session's room events: for each one it
// forwards the event to subscribers, asks the Controller who speaks next,
// and runs that agent. It ends when the session's main room is closed
// (Floor.Stop), closing all subscriber channels. Calling Start on a
// running session does nothing.
func (s *Session) Start() {
	s.loop.startOnce.Do(s.startLoop)
}

// Running reports whether Start has been called.
func (s *Session) Running() bool { return s.loop.started.Load() }

func (s *Session) startLoop() {
	s.loop.started.Store(true)
	unified := s.StartUnified()
	go func() {
		for tagged := range unified {
			s.handle(tagged)
		}
		s.loop.subMu.Lock()
		for _, sub := range s.loop.subscribers {
			close(sub.ch)
		}
		s.loop.subscribers = nil
		s.loop.subMu.Unlock()
	}()
}

// emit delivers an event to every subscriber.
func (s *Session) emit(roomID string, ev ChatEvent) {
	s.loop.subMu.Lock()
	subs := append([]*sessionSubscriber(nil), s.loop.subscribers...)
	s.loop.subMu.Unlock()
	tagged := TaggedEvent{RoomID: roomID, Event: ev}
	for _, sub := range subs {
		select {
		case sub.ch <- tagged:
		case <-sub.done:
		}
	}
}

// handle processes one event from the session's rooms.
func (s *Session) handle(tagged TaggedEvent) {
	// Events for a sub-room that has since closed are dropped.
	view, ctrl := s, s.Controller
	if tagged.RoomID != "" {
		room, ok := s.Rooms[tagged.RoomID]
		if !ok {
			return
		}
		view, ctrl = s.ForRoom(room), room.Controller
	}

	s.emit(tagged.RoomID, tagged.Event)
	if s.loop.stopped {
		return
	}

	switch e := tagged.Event.(type) {
	case MessagePosted, AgentPassedEvent, AgentErrorEvent:
		d := ctrl.Decide(view.MainRoom, e)
		if info := TryAutoCloseRoom(tagged.RoomID, d, s, s.Controller); info != "" {
			s.emit("", InfoEvent{Text: info})
		}
		s.act(tagged.RoomID, view, d)

	case UserCommandEvent:
		d := HandleCommand(e.Command, s, s.Controller)
		switch d.Action {
		case "stop":
			s.stop()
			return
		case "clear":
			s.emit("", SessionCleared{})
		case "room_created", "room_closed", "error":
			s.emit("", InfoEvent{Text: d.Info})
		}
		s.emit("", AwaitingInput{})
	}
}

// act carries out a Controller decision for the room the event came from.
func (s *Session) act(roomID string, view *Session, d Decision) {
	switch d.Action {
	case "trigger":
		agent, err := s.buildAgent(d.AgentID)
		if err != nil {
			s.emit(roomID, InfoEvent{Text: fmt.Sprintf("[ERROR: %v]", err)})
			return
		}
		s.emit(roomID, AgentStarted{AgentID: d.AgentID})
		turn := NewAgentTurn(view, view.MainRoom, s.Floor, d.AgentID)
		// Agent.Run posts its result (message, pass, or error) to the room,
		// which comes back through the loop and drives the next decision.
		go agent.Run(context.Background(), turn)

	case "wait":
		if roomID == "" {
			s.emit("", AwaitingInput{})
		}

	case "stop":
		s.stop()
	}
}

// buildAgent constructs the Agent for one turn from the floor's live spec.
func (s *Session) buildAgent(agentID string) (Agent, error) {
	if s.Floor.AgentFactory == nil {
		return nil, fmt.Errorf("no AgentFactory on the floor, cannot run %s", agentID)
	}
	for _, spec := range s.Floor.Agents() {
		if spec.ID == agentID {
			agent := s.Floor.AgentFactory(&spec)
			if agent == nil {
				return nil, fmt.Errorf("AgentFactory returned no agent for %s", agentID)
			}
			return agent, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %s", agentID)
}

func (s *Session) stop() {
	s.loop.stopped = true
	s.emit("", SessionStopped{})
}
