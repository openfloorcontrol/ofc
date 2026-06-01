package floor

import (
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

func sessionTestBlueprint() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		Name: "session-test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "always", ToolContext: "full"},
		},
	}
}

func TestNewFloorCreatesDefaultSession(t *testing.T) {
	f := NewFloor(sessionTestBlueprint())

	if f.Sessions == nil {
		t.Fatal("Floor.Sessions should be initialized")
	}
	if len(f.Sessions) != 1 {
		t.Errorf("expected exactly 1 session, got %d", len(f.Sessions))
	}

	def := f.DefaultSession()
	if def == nil {
		t.Fatal("DefaultSession() should not return nil")
	}
	if def.ID != "default" {
		t.Errorf("expected default session ID 'default', got %q", def.ID)
	}
	if def.Floor != f {
		t.Error("default session should back-reference its Floor")
	}
}

func TestNewSessionCreatesAgentContexts(t *testing.T) {
	f := NewFloor(sessionTestBlueprint())
	sess := f.DefaultSession()

	if got := len(sess.AgentContexts); got != 2 {
		t.Fatalf("expected 2 agent contexts, got %d", got)
	}
	for _, aid := range []string{"@a", "@b"} {
		if sess.GetAgentContext(aid) == nil {
			t.Errorf("missing AgentContext for %s", aid)
		}
	}
}

func TestSessionChatAndAgentContextWiring(t *testing.T) {
	// Posting to the session's chat should populate each agent's context.
	f := NewFloor(sessionTestBlueprint())
	sess := f.DefaultSession()

	// Drain events so Post doesn't block.
	go func() { for range sess.MainRoom.Events() {} }()

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "ping"})

	if sess.GetAgentContext("@a").Len() != 1 {
		t.Errorf("@a context: expected 1 entry, got %d", sess.GetAgentContext("@a").Len())
	}
	if sess.GetAgentContext("@b").Len() != 1 {
		t.Errorf("@b context: expected 1 entry, got %d", sess.GetAgentContext("@b").Len())
	}
}

func TestMultipleSessionsOnFloorAreIndependent(t *testing.T) {
	// Manually create a second session to verify the model supports it,
	// even though v1 only ever uses the default.
	f := NewFloor(sessionTestBlueprint())
	s1 := f.DefaultSession()
	s2 := NewSession("alt", f)
	f.Sessions["alt"] = s2

	go func() { for range s1.MainRoom.Events() {} }()
	go func() { for range s2.MainRoom.Events() {} }()

	s1.MainRoom.Post(ChatMessage{From: "@user", Content: "in session 1"})
	s2.MainRoom.Post(ChatMessage{From: "@user", Content: "in session 2"})

	// Each session's agents see only their own session's traffic.
	if s1.GetAgentContext("@a").Len() != 1 {
		t.Errorf("s1 @a: expected 1 entry, got %d", s1.GetAgentContext("@a").Len())
	}
	if s2.GetAgentContext("@a").Len() != 1 {
		t.Errorf("s2 @a: expected 1 entry, got %d", s2.GetAgentContext("@a").Len())
	}

	for _, e := range s1.GetAgentContext("@a").Entries() {
		if e.Content == "in session 2" {
			t.Error("s1 should not see s2's messages")
		}
	}
	for _, e := range s2.GetAgentContext("@a").Entries() {
		if e.Content == "in session 1" {
			t.Error("s2 should not see s1's messages")
		}
	}

	// AgentContext maps are physically distinct.
	if s1.AgentContexts["@a"] == s2.AgentContexts["@a"] {
		t.Error("sessions should have distinct AgentContext pointers per agent")
	}
}

func TestSessionsShareFloorResources(t *testing.T) {
	// Sessions on the same Floor share furniture, sandbox, etc. via Floor back-ref.
	f := NewFloor(sessionTestBlueprint())
	s1 := f.DefaultSession()
	s2 := NewSession("alt", f)
	f.Sessions["alt"] = s2

	if s1.Floor != s2.Floor {
		t.Error("sessions on same Floor should share Floor pointer")
	}
}

func TestSessionForRoomReturnsShallowView(t *testing.T) {
	f := NewFloor(sessionTestBlueprint())
	sess := f.DefaultSession()
	go func() { for range sess.MainRoom.Events() {} }()

	room, err := sess.CreateRoom("#r", "@user", []string{"@a"}, "")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Events() {} }()

	view := sess.ForRoom(room)
	if view.MainRoom != room {
		t.Error("view.MainRoom should be room")
	}
	if view.Floor != sess.Floor {
		t.Error("view should share Floor with parent")
	}
	// AgentContexts and Rooms maps are shared (not copied)
	if &view.AgentContexts == &sess.AgentContexts {
		// Comparing addresses of map headers — they're separate locals.
		// The check that matters is identity of the underlying map:
		// modifying one should be visible in the other. Spot-check:
	}
	if view.AgentContexts["@a"] != sess.AgentContexts["@a"] {
		t.Error("view should share AgentContext pointers with parent")
	}
}
