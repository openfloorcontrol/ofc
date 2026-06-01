package floor

import (
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

func mutationTestBlueprint() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		Name: "mut-test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "mention", ToolContext: "full"},
		},
	}
}

func TestNewFloorAppliesLLMAgents(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())

	if got := len(f.Agents); got != 2 {
		t.Fatalf("expected 2 agents on floor, got %d", got)
	}
	sess := f.DefaultSession()
	if got := len(sess.AgentContexts); got != 2 {
		t.Errorf("expected 2 AgentContexts in default session, got %d", got)
	}
}

func TestAddAgentDuplicateRejected(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())

	err := f.AddAgent(blueprint.Agent{ID: "@a"}, func(string) {})
	if err == nil {
		t.Fatal("expected error adding duplicate agent, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

func TestAddAgentPropagatesToAllSessions(t *testing.T) {
	f := NewFloor(&blueprint.Blueprint{Name: "empty"}) // no agents in blueprint
	// Create a second session BEFORE adding the agent.
	s2 := NewSession("alt", f)
	f.Sessions["alt"] = s2

	if err := f.AddAgent(blueprint.Agent{ID: "@x", Activation: "always"}, func(string) {}); err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}

	if f.DefaultSession().GetAgentContext("@x") == nil {
		t.Error("default session missing AgentContext for @x")
	}
	if s2.GetAgentContext("@x") == nil {
		t.Error("alt session missing AgentContext for @x")
	}
}

func TestRemoveAgentPropagatesToAllSessions(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())
	s2 := NewSession("alt", f)
	f.Sessions["alt"] = s2

	if err := f.RemoveAgent("@a"); err != nil {
		t.Fatalf("RemoveAgent failed: %v", err)
	}

	if f.DefaultSession().GetAgentContext("@a") != nil {
		t.Error("default session still has AgentContext for @a")
	}
	if s2.GetAgentContext("@a") != nil {
		t.Error("alt session still has AgentContext for @a")
	}
	// Floor.Agents should no longer contain @a
	for _, a := range f.Agents {
		if a.ID == "@a" {
			t.Error("Floor.Agents still contains @a after RemoveAgent")
		}
	}
}

func TestRemoveAgentNotFound(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())
	err := f.RemoveAgent("@nonexistent")
	if err == nil {
		t.Fatal("expected error removing nonexistent agent, got nil")
	}
}

func TestUpdateAgentMutatesInPlace(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())

	if err := f.UpdateAgent("@a", func(a *blueprint.Agent) {
		a.Prompt = "new prompt"
		a.Temperature = 0.9
	}); err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}

	var found *blueprint.Agent
	for i := range f.Agents {
		if f.Agents[i].ID == "@a" {
			found = &f.Agents[i]
			break
		}
	}
	if found == nil {
		t.Fatal("agent @a not found after update")
	}
	if found.Prompt != "new prompt" {
		t.Errorf("expected prompt 'new prompt', got %q", found.Prompt)
	}
	if found.Temperature != 0.9 {
		t.Errorf("expected temperature 0.9, got %v", found.Temperature)
	}
}

func TestUpdateAgentNotFound(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())
	err := f.UpdateAgent("@nonexistent", func(a *blueprint.Agent) {})
	if err == nil {
		t.Fatal("expected error updating nonexistent agent, got nil")
	}
}

func TestAddAgentACPRequiresStart(t *testing.T) {
	f := NewFloor(&blueprint.Blueprint{Name: "test"})
	err := f.AddAgent(blueprint.Agent{ID: "@acp", Type: "acp", Command: "echo"}, func(string) {})
	if err == nil {
		t.Fatal("expected error adding ACP agent before Start(), got nil")
	}
	if !strings.Contains(err.Error(), "Start()") {
		t.Errorf("expected error mentioning Start(), got: %v", err)
	}
}

func TestSetBlueprintMeta(t *testing.T) {
	f := NewFloor(mutationTestBlueprint())
	f.SetBlueprintMeta("renamed", "new description")
	if f.Blueprint.Name != "renamed" {
		t.Errorf("expected name 'renamed', got %q", f.Blueprint.Name)
	}
	if f.Blueprint.Description != "new description" {
		t.Errorf("expected description 'new description', got %q", f.Blueprint.Description)
	}
}

func TestAddAgentContextPropagationToChat(t *testing.T) {
	// After AddAgent, posting to the session's chat should populate
	// the new agent's context.
	f := NewFloor(&blueprint.Blueprint{Name: "test"})
	sess := f.DefaultSession()
	go func() { for range sess.Chat.Events() {} }()

	if err := f.AddAgent(blueprint.Agent{ID: "@new", Activation: "always"}, func(string) {}); err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}
	sess.Chat.Post(ChatMessage{From: "@user", Content: "hello"})

	ctx := sess.GetAgentContext("@new")
	if ctx == nil {
		t.Fatal("@new missing AgentContext")
	}
	if ctx.Len() != 1 {
		t.Errorf("expected 1 entry in @new's context, got %d", ctx.Len())
	}
}

func TestRemoveAgentStopsContextUpdates(t *testing.T) {
	// After RemoveAgent, posting to the session's chat should NOT
	// reach the removed agent's (former) context.
	f := NewFloor(mutationTestBlueprint())
	sess := f.DefaultSession()
	go func() { for range sess.Chat.Events() {} }()

	// Grab a reference to @a's context before removing
	ctxA := sess.GetAgentContext("@a")
	if ctxA == nil {
		t.Fatal("@a context missing before remove")
	}

	if err := f.RemoveAgent("@a"); err != nil {
		t.Fatalf("RemoveAgent failed: %v", err)
	}

	sess.Chat.Post(ChatMessage{From: "@user", Content: "after remove"})

	// @a's old context shouldn't see the new message.
	for _, e := range ctxA.Entries() {
		if e.Content == "after remove" {
			t.Error("@a's old context received a message after RemoveAgent")
		}
	}
}
