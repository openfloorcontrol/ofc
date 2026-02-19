package floor

import (
	"fmt"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

func twoAgentBlueprint() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@data", Activation: "always", ToolContext: "full"},
			{ID: "@code", Activation: "mention", ToolContext: "full"},
		},
	}
}

// requireEvent asserts that events[index] is of type T and returns it.
func requireEvent[T Event](t *testing.T, events []Event, index int) T {
	t.Helper()
	if index >= len(events) {
		t.Fatalf("expected event at index %d, but only got %d events", index, len(events))
	}
	e, ok := events[index].(T)
	if !ok {
		t.Fatalf("expected event[%d] to be %T, got %T", index, e, events[index])
	}
	return e
}

func TestUserMessageTriggersAlwaysAgent(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())
	events := ctrl.HandleEvent(UserMessage{Content: "hello"})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	pa := requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@data" {
		t.Errorf("expected @data, got %s", pa.AgentID)
	}
}

func TestMentionDelegation(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// User says hello → @data wakes (always activation)
	events := ctrl.HandleEvent(UserMessage{Content: "hello"})
	requireEvent[PromptAgent](t, events, 0)

	// @data mentions @code? → should delegate to @code
	events = ctrl.HandleEvent(AgentDone{
		AgentID: "@data",
		Content: "Let me ask @code? about this",
	})
	pa := requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@code" {
		t.Errorf("expected @code, got %s", pa.AgentID)
	}

	// Verify call stack was pushed
	if len(ctrl.CallStack) != 1 {
		t.Fatalf("expected stack depth 1, got %d", len(ctrl.CallStack))
	}
	if ctrl.CallStack[0].Caller != "@data" || ctrl.CallStack[0].Callee != "@code" {
		t.Errorf("unexpected frame: %+v", ctrl.CallStack[0])
	}
}

func TestStackPopReturns(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// User → @data wakes
	ctrl.HandleEvent(UserMessage{Content: "hello"})

	// @data mentions @code? → delegates
	ctrl.HandleEvent(AgentDone{
		AgentID: "@data",
		Content: "ask @code? about this",
	})

	// @code responds (no mentions) → stack pops, returns to @data
	events := ctrl.HandleEvent(AgentDone{
		AgentID: "@code",
		Content: "here is the result",
	})
	pa := requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@data" {
		t.Errorf("expected @data (stack pop), got %s", pa.AgentID)
	}

	// Stack should be empty now
	if len(ctrl.CallStack) != 0 {
		t.Errorf("expected empty stack, got %d", len(ctrl.CallStack))
	}
}

func TestStackPopToUser(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// User mentions @code? directly
	events := ctrl.HandleEvent(UserMessage{Content: "@code? what is this?"})
	pa := requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@code" {
		t.Errorf("expected @code, got %s", pa.AgentID)
	}

	// @code responds → stack pops to @user → WaitingForUser
	events = ctrl.HandleEvent(AgentDone{
		AgentID: "@code",
		Content: "it's a test",
	})
	requireEvent[WaitingForUser](t, events, 0)
}

func TestPassExcludesAgent(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// User says hello → @data wakes
	events := ctrl.HandleEvent(UserMessage{Content: "hello"})
	requireEvent[PromptAgent](t, events, 0)

	// @data passes → should try next agent or go back to user
	events = ctrl.HandleEvent(AgentPassed{AgentID: "@data"})
	// @code has "mention" activation, won't wake → back to user
	requireEvent[WaitingForUser](t, events, 0)
}

func TestPassAfterMentionFallsBack(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "always", ToolContext: "full"},
		},
	}
	ctrl := NewController(bp)

	// User says hello → @a wakes (first always agent)
	events := ctrl.HandleEvent(UserMessage{Content: "hello"})
	pa := requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@a" {
		t.Errorf("expected @a, got %s", pa.AgentID)
	}

	// @a passes → @b should wake (second always agent)
	events = ctrl.HandleEvent(AgentPassed{AgentID: "@a"})
	pa = requireEvent[PromptAgent](t, events, 0)
	if pa.AgentID != "@b" {
		t.Errorf("expected @b, got %s", pa.AgentID)
	}
}

func TestQuitCommand(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())
	events := ctrl.HandleEvent(UserCommand{Command: "/quit"})
	requireEvent[FloorStopped](t, events, 0)
}

func TestClearCommand(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// Add some messages
	ctrl.HandleEvent(UserMessage{Content: "hello"})
	ctrl.HandleEvent(AgentDone{AgentID: "@data", Content: "hi"})

	if len(ctrl.Messages) == 0 {
		t.Fatal("expected messages before clear")
	}

	events := ctrl.HandleEvent(UserCommand{Command: "/clear"})
	requireEvent[ConversationCleared](t, events, 0)

	if len(ctrl.Messages) != 0 {
		t.Errorf("expected empty messages after clear, got %d", len(ctrl.Messages))
	}
}

func TestUnknownCommand(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())
	events := ctrl.HandleEvent(UserCommand{Command: "/foo"})
	si := requireEvent[SystemInfo](t, events, 0)
	if si.Text != "Unknown command: /foo" {
		t.Errorf("unexpected system info: %s", si.Text)
	}
}

func TestAgentErrorReturnsToUser(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())
	events := ctrl.HandleEvent(AgentError{
		AgentID: "@data",
		Err:     fmt.Errorf("connection timeout"),
	})
	// Should emit SystemInfo + WaitingForUser
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	requireEvent[SystemInfo](t, events, 0)
	requireEvent[WaitingForUser](t, events, 1)
}

func TestMentionsUserPausesForUser(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	// User says hello → @data wakes
	ctrl.HandleEvent(UserMessage{Content: "hello"})

	// @data mentions @user? → should pause for user input
	events := ctrl.HandleEvent(AgentDone{
		AgentID: "@data",
		Content: "I need to ask @user? about this",
	})
	requireEvent[WaitingForUser](t, events, 0)
}

func TestToolInteractionsPreserved(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())

	ctrl.HandleEvent(UserMessage{Content: "do something"})
	ctrl.HandleEvent(AgentDone{
		AgentID: "@data",
		Content: "done",
		ToolInteractions: []ToolInteraction{
			{Command: "ls -la", Output: "file1\nfile2"},
		},
	})

	if len(ctrl.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ctrl.Messages))
	}
	msg := ctrl.Messages[1]
	if len(msg.ToolInteractions) != 1 {
		t.Fatalf("expected 1 tool interaction, got %d", len(msg.ToolInteractions))
	}
	if msg.ToolInteractions[0].Command != "ls -la" {
		t.Errorf("unexpected command: %s", msg.ToolInteractions[0].Command)
	}
}

func TestNoMessagesReturnsNil(t *testing.T) {
	ctrl := NewController(twoAgentBlueprint())
	// Calling advanceTurn with no messages should return WaitingForUser
	events := ctrl.advanceTurn()
	requireEvent[WaitingForUser](t, events, 0)
}
