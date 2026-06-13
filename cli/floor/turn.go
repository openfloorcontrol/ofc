package floor

import (
	"fmt"

	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// agentTurn is the engine-side implementation of AgentTurn. It binds an
// agent to a specific session view + target room for a single Run()
// invocation, and provides scoped access to engine resources.
//
// One agentTurn per Agent.Run call. The agent's identity, the room it
// posts into, and the session it reads from are all fixed at
// construction; the agent doesn't (and can't) navigate to siblings.
type agentTurn struct {
	view    SessionView
	room    *Room
	floor   *Floor
	agentID string
}

// NewAgentTurn constructs a turn for an agent dispatch. Used by the
// frontend dispatch helpers (CLI, TUI, JSON, RunOnce) and by anywhere
// else that triggers Agent.Run.
//
// view: the session (or room-scoped view) the agent is running inside.
// room: where Stream/Status/Reply land. Usually view.MainRoom — for
//
//	sub-room dispatch, the sub-room.
//
// floor: provides access to furniture, sandbox, ACP subprocesses, debug.
// agentID: the agent's ID.
func NewAgentTurn(view SessionView, room *Room, floor *Floor, agentID string) AgentTurn {
	return &agentTurn{view: view, room: room, floor: floor, agentID: agentID}
}

func (t *agentTurn) AgentID() string { return t.agentID }

func (t *agentTurn) Entries() []*ChatMessage {
	events, _ := t.view.Store().ReadForAgent(t.view.ID(), t.agentID, EventFilter{})
	return extractMessages(events)
}

func (t *agentTurn) Delta() []*ChatMessage {
	// AgentContext owns the sentSeq cursor for per-agent delta reads.
	// Route through it so the cursor stays consistent across calls.
	ac := t.contextOrNil()
	if ac == nil {
		return t.Entries()
	}
	return ac.Delta()
}

func (t *agentTurn) MarkSent() {
	if ac := t.contextOrNil(); ac != nil {
		ac.MarkSent()
	}
}

func (t *agentTurn) Stream(ev Event)        { t.room.PostStream(ev) }
func (t *agentTurn) Status(ev ChatEvent)    { t.room.PostEvent(ev) }
func (t *agentTurn) Reply(msg ChatMessage)  { t.room.Post(msg) }

func (t *agentTurn) Furniture(name string) (furniture.Furniture, bool) {
	f, ok := t.floor.Furniture[name]
	return f, ok
}

func (t *agentTurn) Sandbox() *sandbox.Sandbox {
	return t.floor.Sandbox
}

func (t *agentTurn) ACPSubprocess() (*acpclient.Subprocess, bool) {
	sub, ok := t.floor.ACPSubprocesses[t.agentID]
	return sub, ok
}

func (t *agentTurn) Debug(format string, args ...any) {
	if t.floor.DebugFunc != nil {
		t.floor.DebugFunc(fmt.Sprintf(format, args...))
	}
}

// contextOrNil returns the AgentContext for this agent inside the
// session, or nil if one was never registered (e.g., test fixtures).
// We have to reach through Session for this — the cursor lives there.
func (t *agentTurn) contextOrNil() *AgentContext {
	sess, ok := t.view.(*Session)
	if !ok {
		return nil
	}
	return sess.GetAgentContext(t.agentID)
}
