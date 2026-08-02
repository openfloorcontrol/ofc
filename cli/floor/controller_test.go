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

func requireDecision(t *testing.T, d Decision, action string, agentID string) {
	t.Helper()
	if d.Action != action {
		t.Fatalf("expected action %q, got %q (info: %s)", action, d.Action, d.Info)
	}
	if agentID != "" && d.AgentID != agentID {
		t.Fatalf("expected agent %q, got %q", agentID, d.AgentID)
	}
}

func TestDecideUserMessageTriggersAlwaysAgent(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})

	requireDecision(t, d, "trigger", "@data")
}

func TestDecideMentionDelegation(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})
	requireDecision(t, d, "trigger", "@data")

	// @data mentions @code?
	chat.Post(ChatMessage{From: "@data", Content: "Let me ask @code? about this"})
	d = ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@data", Content: "Let me ask @code? about this"}})
	requireDecision(t, d, "trigger", "@code")

	// Verify call stack
	if len(ctrl.CallStack) != 1 {
		t.Fatalf("expected stack depth 1, got %d", len(ctrl.CallStack))
	}
}

func TestDecideStackPopReturns(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})

	// @data → @code
	chat.Post(ChatMessage{From: "@data", Content: "ask @code? about this"})
	ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@data", Content: "ask @code? about this"}})

	// @code responds → pops to @data
	chat.Post(ChatMessage{From: "@code", Content: "here is the result"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@code", Content: "here is the result"}})
	requireDecision(t, d, "trigger", "@data")

	if len(ctrl.CallStack) != 0 {
		t.Errorf("expected empty stack, got %d", len(ctrl.CallStack))
	}
}

func TestDecideStackPopToUser(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "@code? what is this?"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "@code? what is this?"}})
	requireDecision(t, d, "trigger", "@code")

	// @code responds → pops to @user → wait
	chat.Post(ChatMessage{From: "@code", Content: "it's a test"})
	d = ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@code", Content: "it's a test"}})
	requireDecision(t, d, "wait", "")
}

func TestDecidePassExcludesAgent(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})
	requireDecision(t, d, "trigger", "@data")

	// @data passes → @code won't wake (mention only) → wait
	d = ctrl.Decide(chat, AgentPassedEvent{AgentID: "@data"})
	requireDecision(t, d, "wait", "")
}

func TestDecidePassFallsToNextAlwaysAgent(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "always", ToolContext: "full"},
		},
	}
	f := NewFloor(bp)
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()

	// User says hello → @a
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})
	requireDecision(t, d, "trigger", "@a")

	// @a passes → @b
	d = ctrl.Decide(chat, AgentPassedEvent{AgentID: "@a"})
	requireDecision(t, d, "trigger", "@b")
}

func TestDecideQuitCommand(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	sess := f.DefaultSession()

	d := HandleCommand("/quit", sess, ctrl)
	requireDecision(t, d, "stop", "")
}

func TestDecideClearCommand(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	sess := f.DefaultSession()

	// Add some messages
	sess.MainRoom.Post(ChatMessage{From: "@user", Content: "hello"})
	ctrl.Decide(sess.MainRoom, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})

	if len(sess.MainRoom.History()) == 0 {
		t.Fatal("expected messages before clear")
	}

	d := HandleCommand("/clear", sess, ctrl)
	requireDecision(t, d, "clear", "")

	if len(sess.MainRoom.History()) != 0 {
		t.Errorf("expected empty after clear, got %d", len(sess.MainRoom.History()))
	}
}

// An errored agent ends its turn like a passed one: turn-taking moves on
// rather than returning an action the event loop has no rule for. Frontends
// surface the failure from AgentErrorEvent itself.
func TestDecideAgentErrorHandsBackControl(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})

	d := ctrl.Decide(chat, AgentErrorEvent{
		AgentID: "@data",
		Err:     fmt.Errorf("connection timeout"),
	})

	// @code is mention-only, so nobody else wakes — control goes to the user.
	requireDecision(t, d, "wait", "")
}

// A failing agent must not be woken again on the next poll, or an endpoint
// that is simply down would spin.
func TestDecideAgentErrorExcludesAgent(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "always", ToolContext: "full"},
		},
	}
	f := NewFloor(bp)
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})

	d := ctrl.Decide(chat, AgentErrorEvent{AgentID: "@a", Err: fmt.Errorf("boom")})
	requireDecision(t, d, "trigger", "@b")

	d = ctrl.Decide(chat, AgentErrorEvent{AgentID: "@b", Err: fmt.Errorf("boom")})
	requireDecision(t, d, "wait", "")
}

// A delegated agent that errors returns control to its caller instead of
// stranding the floor mid-delegation.
func TestDecideAgentErrorPopsCallStack(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()

	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})
	requireDecision(t, d, "trigger", "@data")

	// @data delegates to @code
	msg := ChatMessage{From: "@data", Content: "@code? please build it"}
	chat.Post(msg)
	d = ctrl.Decide(chat, MessagePosted{Message: msg})
	requireDecision(t, d, "trigger", "@code")
	if len(ctrl.CallStack) != 1 {
		t.Fatalf("expected a call frame after delegation, got %d", len(ctrl.CallStack))
	}

	// @code errors → frame popped
	d = ctrl.Decide(chat, AgentErrorEvent{AgentID: "@code", Err: fmt.Errorf("boom")})
	if len(ctrl.CallStack) != 0 {
		t.Errorf("expected the call frame to be popped, got %d", len(ctrl.CallStack))
	}
	if d.Action == "error" {
		t.Errorf("decision should describe what happens next, got %q", d.Action)
	}
}

func TestDecideMentionsUserPauses(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "hello"}})

	// @data mentions @user? → wait
	chat.Post(ChatMessage{From: "@data", Content: "I need to ask @user? about this"})
	d := ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@data", Content: "I need to ask @user? about this"}})
	requireDecision(t, d, "wait", "")
}

func TestDecideUnknownCommand(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	sess := f.DefaultSession()

	d := HandleCommand("/foo", sess, ctrl)
	requireDecision(t, d, "error", "")
	if d.Info != "Unknown command: /foo" {
		t.Errorf("unexpected info: %s", d.Info)
	}
}

func TestDecideToolInteractionsPreserved(t *testing.T) {
	f := NewFloor(twoAgentBlueprint())
	ctrl := NewController(f)
	chat := f.DefaultSession().MainRoom
	go func() {
		for range chat.Events() {
		}
	}()
	chat.Post(ChatMessage{From: "@user", Content: "do something"})
	ctrl.Decide(chat, MessagePosted{Message: ChatMessage{From: "@user", Content: "do something"}})

	chat.Post(ChatMessage{
		From:    "@data",
		Content: "done",
		ToolInteractions: []ToolInteraction{
			{Command: "ls -la", Output: "file1\nfile2"},
		},
	})

	history := chat.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	msg := history[1]
	if len(msg.ToolInteractions) != 1 {
		t.Fatalf("expected 1 tool interaction, got %d", len(msg.ToolInteractions))
	}
	if msg.ToolInteractions[0].Command != "ls -la" {
		t.Errorf("unexpected command: %s", msg.ToolInteractions[0].Command)
	}
}
