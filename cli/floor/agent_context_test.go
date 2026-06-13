package floor

import (
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

func acTestBlueprint(agentIDs ...string) *blueprint.Blueprint {
	bp := &blueprint.Blueprint{Name: "ac-test"}
	for _, id := range agentIDs {
		bp.Agents = append(bp.Agents, blueprint.Agent{ID: id, Activation: "always"})
	}
	return bp
}

// drainEvents reads from sess.MainRoom.Events() so Post doesn't block on
// the channel buffer (capacity 64).
func drainMain(sess *Session) {
	go func() {
		for range sess.MainRoom.Events() {
		}
	}()
}

func TestAgentContextAccumulatesMessages(t *testing.T) {
	f := NewFloor(acTestBlueprint("@test"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac := sess.GetAgentContext("@test")

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "hello"})

	entries := ac.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", entries[0].Content)
	}
	if entries[0].From != "@user" {
		t.Errorf("expected '@user', got %q", entries[0].From)
	}
}

func TestAgentContextMultipleMessages(t *testing.T) {
	f := NewFloor(acTestBlueprint("@test"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac := sess.GetAgentContext("@test")

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "first"})
	sess.MainRoom.Post(ChatMessage{From: "@agent", Content: "second"})
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "third"})

	entries := ac.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Content != "first" || entries[1].Content != "second" || entries[2].Content != "third" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

func TestAgentContextDelta(t *testing.T) {
	f := NewFloor(acTestBlueprint("@test"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac := sess.GetAgentContext("@test")

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "first"})

	// First delta should include everything seen so far.
	delta := ac.Delta()
	if len(delta) != 1 {
		t.Fatalf("expected 1 delta entry, got %d", len(delta))
	}

	ac.MarkSent()

	// Delta should now be empty.
	delta = ac.Delta()
	if len(delta) != 0 {
		t.Fatalf("expected 0 delta after MarkSent, got %d", len(delta))
	}

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "second"})

	// Delta should have only the new message.
	delta = ac.Delta()
	if len(delta) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(delta))
	}
	if delta[0].Content != "second" {
		t.Errorf("expected 'second', got %q", delta[0].Content)
	}

	// Full Entries should have both.
	entries := ac.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 total entries, got %d", len(entries))
	}
}

func TestAgentContextAppendSystem(t *testing.T) {
	f := NewFloor(acTestBlueprint("@test"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac := sess.GetAgentContext("@test")
	ac.AppendSystem("You moved to #analysis with @code")

	entries := ac.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].From != "@system" {
		t.Errorf("expected @system, got %q", entries[0].From)
	}
	if entries[0].Content != "You moved to #analysis with @code" {
		t.Errorf("unexpected content: %q", entries[0].Content)
	}
}

func TestAgentContextClearRoomHistory(t *testing.T) {
	// In the store-backed model, "clear" lives at the room level
	// (Room.Clear deletes events). AgentContext.Clear just resets
	// sentSeq. Verify both behaviors.
	f := NewFloor(acTestBlueprint("@test"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac := sess.GetAgentContext("@test")
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "hello"})
	ac.MarkSent()

	if ac.Len() != 1 {
		t.Fatalf("expected 1 entry before clear, got %d", ac.Len())
	}

	// Clear the room: removes events from the store, so Entries drops to 0.
	sess.MainRoom.Clear()

	if ac.Len() != 0 {
		t.Errorf("expected 0 entries after room Clear, got %d", ac.Len())
	}

	// AgentContext.Clear resets sentSeq so Delta returns from-scratch.
	ac.Clear()
	delta := ac.Delta()
	if len(delta) != 0 {
		t.Errorf("expected empty delta after Clear (no events), got %d", len(delta))
	}
}

func TestMultipleAgentContextsOnSameRoom(t *testing.T) {
	f := NewFloor(acTestBlueprint("@agent1", "@agent2"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac1 := sess.GetAgentContext("@agent1")
	ac2 := sess.GetAgentContext("@agent2")

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "hello everyone"})

	if ac1.Len() != 1 {
		t.Errorf("ac1: expected 1 entry, got %d", ac1.Len())
	}
	if ac2.Len() != 1 {
		t.Errorf("ac2: expected 1 entry, got %d", ac2.Len())
	}

	// Agent1 gets a private system message.
	ac1.AppendSystem("You moved to #room1")

	if ac1.Len() != 2 {
		t.Errorf("ac1: expected 2 entries after system msg, got %d", ac1.Len())
	}
	if ac2.Len() != 1 {
		t.Errorf("ac2: should still have 1 entry (private msg not visible), got %d", ac2.Len())
	}
}

func TestAgentContextRemoveAgentStopsVisibility(t *testing.T) {
	// After RemoveAgentContext, posts to the main room no longer
	// target this agent. Their existing events remain in the store
	// (audit) but no new events get visibility refs for them.
	f := NewFloor(acTestBlueprint("@agent1", "@agent2"))
	sess := f.DefaultSession()
	drainMain(sess)

	ac1 := sess.GetAgentContext("@agent1")

	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "before"})
	if ac1.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", ac1.Len())
	}

	sess.RemoveAgentContext("@agent1")
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "after"})

	// Read from store directly (ac1 is no longer in the session map,
	// but its events from before-remove are still there).
	events, _ := f.Store.ReadForAgent(sess.ID(), "@agent1", EventFilter{})
	if len(events) != 1 {
		t.Errorf("expected 1 event for @agent1 (only the 'before' one), got %d", len(events))
	}
}
