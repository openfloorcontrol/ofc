package floor

import (
	"github.com/openfloorcontrol/ofc/blueprint"
	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// AgentTurn is the per-turn capability handle passed to Agent.Run. It
// gives an agent the small set of things it actually needs (read context,
// emit results, look up scoped capabilities) without exposing the
// engine's internals.
//
// Construction is the engine's responsibility — see agentTurn / Floor.NewTurn.
// One turn per Run() call: the agent and the target room are bound at
// construction time.
type AgentTurn interface {
	// Identity.
	AgentID() string

	// Context views. LLM agents read Entries (full history); ACP agents
	// read Delta + MarkSent (incremental).
	Entries() []*ChatMessage
	Delta() []*ChatMessage
	MarkSent()

	// Emit results into the bound room.
	Stream(ev Event)
	Status(ev ChatEvent)
	Reply(msg ChatMessage)

	// Capabilities reachable from this turn. Returning a bool lets the
	// caller distinguish "not configured" from "exists but empty".
	Furniture(name string) (furniture.Furniture, bool)
	Sandbox() *sandbox.Sandbox
	ACPSubprocess() (*acpclient.Subprocess, bool)

	// Debug logging, routed through the engine's debug sink.
	Debug(format string, args ...any)
}

// AgentRegistry is the live agent set as seen by Controller and Session.
// Implemented by Floor.
type AgentRegistry interface {
	Agents() []blueprint.Agent
}

// SessionView is the small surface that Room and AgentContext consume
// from the Session they belong to. Implemented by Session. Defined here
// so Room and AgentContext can hold a SessionView instead of a *Session
// back-pointer.
type SessionView interface {
	ID() string
	Store() SessionStore
	AgentsInRoom(roomID string) []string
	AgentRoom(agentID string) string
}
