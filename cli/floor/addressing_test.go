package floor

import (
	"context"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// addressingFloor has @hiro (always), @maya (mention) and @quiet (never),
// a controller and a drained main room.
func addressingFloor(t *testing.T) (*Controller, *Room) {
	t.Helper()
	f := NewFloor(&blueprint.Blueprint{Name: "test", Agents: []blueprint.Agent{
		{ID: "@hiro", Activation: "always"},
		{ID: "@maya", Activation: "mention"},
		{ID: "@quiet", Activation: "never"},
	}})
	room := f.DefaultSession().MainRoom
	go func() {
		for range room.Events() {
		}
	}()
	return NewController(f), room
}

// post adds a message to the room and lets the controller observe it.
func post(ctrl *Controller, room *Room, from, content string) {
	msg := ChatMessage{From: from, Content: content}
	room.Post(msg)
	ctrl.Observe(MessagePosted{Message: msg})
}

func TestNextFollowsTheAddress(t *testing.T) {
	ctrl, room := addressingFloor(t)

	post(ctrl, room, "@user", "Which Nikon do you have?")
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressChime, AgentID: "@maya"}), "trigger", "@maya")

	post(ctrl, room, "@maya", "Ask Hiro.")
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressCall, AgentID: "@hiro"}), "trigger", "@hiro")

	// Hiro answers; someone is waiting for him (Maya), so a chime-in
	// waits and the floor returns to the caller.
	post(ctrl, room, "@hiro", "The D750.")
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressChime, AgentID: "@quiet"}), "trigger", "@maya")

	post(ctrl, room, "@maya", "There you go.")
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressNobody}), "wait", "")
}

func TestNextRefusesWhoMayNotSpeak(t *testing.T) {
	ctrl, room := addressingFloor(t)
	post(ctrl, room, "@hiro", "Anyone?")

	for _, addr := range []Address{
		{Kind: AddressCall, AgentID: "@quiet"},  // activation never
		{Kind: AddressCall, AgentID: "@hiro"},   // wrote the last message
		{Kind: AddressCall, AgentID: "@nobody"}, // not on the floor
		{Kind: AddressChime, AgentID: "@quiet"}, // activation never
	} {
		requireDecision(t, ctrl.Next(room, addr), "wait", "")
	}
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressCall, AgentID: "@user"}), "wait", "")
}

func TestPassedAgentIsNotOffered(t *testing.T) {
	ctrl, room := addressingFloor(t)
	post(ctrl, room, "@user", "hi")
	ctrl.Observe(AgentPassedEvent{AgentID: "@hiro"})

	for _, a := range ctrl.Request(room).Agents {
		if a.ID == "@hiro" || a.ID == "@quiet" {
			t.Errorf("request offers %s", a.ID)
		}
	}
	requireDecision(t, ctrl.Next(room, Address{Kind: AddressChime, AgentID: "@hiro"}), "wait", "")
}

// Two "always" agents keep answering each other; max_agent_turns ends it
// and the next user message starts a new count.
func TestMaxAgentTurnsEndsPingPong(t *testing.T) {
	f := NewFloor(&blueprint.Blueprint{Name: "test", Agents: []blueprint.Agent{
		{ID: "@hiro", Activation: "always"},
		{ID: "@maya", Activation: "always"},
	}})
	room := f.DefaultSession().MainRoom
	go func() {
		for range room.Events() {
		}
	}()
	ctrl := NewController(f)
	ctrl.MaxAgentTurns = 3
	step := func(from string) Decision {
		msg := ChatMessage{From: from, Content: "I disagree."}
		room.Post(msg)
		return ctrl.Decide(room, MessagePosted{Message: msg})
	}

	requireDecision(t, step("@user"), "trigger", "@hiro")
	requireDecision(t, step("@hiro"), "trigger", "@maya")
	requireDecision(t, step("@maya"), "trigger", "@hiro")
	d := step("@hiro")
	requireDecision(t, d, "wait", "")
	if d.Info == "" {
		t.Error("reaching max_agent_turns should say so")
	}
	requireDecision(t, step("@user"), "trigger", "@hiro")
}

func TestOnlyTheLatestDecisionIsCurrent(t *testing.T) {
	ctrl, room := addressingFloor(t)
	post(ctrl, room, "@user", "one")
	first := ctrl.Request(room).Seq
	post(ctrl, room, "@user", "two")
	if ctrl.Current(first) || !ctrl.Current(ctrl.Request(room).Seq) {
		t.Error("only the latest decision should be current")
	}
}

func TestMentionAddresser(t *testing.T) {
	agents := []blueprint.Agent{{ID: "@hiro", Activation: "always"}, {ID: "@maya", Activation: "mention"}}
	cases := []struct {
		from, content string
		want          Address
	}{
		{"@user", "@maya? what's the sensor?", Address{Kind: AddressCall, AgentID: "@maya"}},
		{"@maya", "Not sure, @user?", Address{Kind: AddressCall, AgentID: "@user"}},
		{"@user", "hello", Address{Kind: AddressChime, AgentID: "@hiro"}},
		{"@hiro", "Hello there", Address{Kind: AddressNobody}},
		{"@hiro", "@hiro? talking to myself", Address{Kind: AddressNobody}},
	}
	for _, c := range cases {
		req := AddressRequest{History: []ChatMessage{{From: c.from, Content: c.content}}, Agents: agents}
		got := MentionAddresser{}.Address(context.Background(), req)
		if got.Kind != c.want.Kind || got.AgentID != c.want.AgentID {
			t.Errorf("%s: %q → %s %s, want %s %s", c.from, c.content, got.Kind, got.AgentID, c.want.Kind, c.want.AgentID)
		}
	}
}
