package floor

// EventContext is the (session, controller) pair that should handle a
// given event from the unified channel. For events from the main room
// it's the session's own (Sess, Ctrl); for sub-room events it's the
// room-scoped view (Sess.ForRoom(room), room.Controller).
type EventContext struct {
	Sess   *Session
	Ctrl   *Controller
	RoomID string // "" for the main room
}

// ResolveEventContext picks the (Session, Controller) pair that should
// handle an event from the unified channel. Returns ok=false if the
// event refers to a sub-room that no longer exists (stale event after
// the room closed); callers should skip such events.
func ResolveEventContext(sess *Session, ctrl *Controller, tagged TaggedEvent) (EventContext, bool) {
	if tagged.RoomID == "" {
		return EventContext{Sess: sess, Ctrl: ctrl, RoomID: ""}, true
	}
	room, ok := sess.Rooms[tagged.RoomID]
	if !ok {
		return EventContext{}, false
	}
	return EventContext{
		Sess:   sess.ForRoom(room),
		Ctrl:   room.Controller,
		RoomID: tagged.RoomID,
	}, true
}

// DecideAndAutoClose runs the standard "process a chat event" sequence:
// call Controller.Decide, then check whether the resulting Decision
// should auto-close the current room. If a room was closed, onClose is
// called with the info text (frontends typically render it as a system
// message).
//
// Callers act on the returned Decision (trigger an agent, signal
// readiness for user input, terminate in one-shot mode, etc.).
//
// sess and ctrl are the session-level (not room-scoped) references —
// they're needed for TryAutoCloseRoom which mutates the session's
// rooms map.
func DecideAndAutoClose(
	ec EventContext,
	ev ChatEvent,
	sess *Session,
	ctrl *Controller,
	onClose func(info string),
) Decision {
	decision := ec.Ctrl.Decide(ec.Sess.MainRoom, ev)
	if info := TryAutoCloseRoom(ec.RoomID, decision, sess, ctrl); info != "" && onClose != nil {
		onClose(info)
	}
	return decision
}
