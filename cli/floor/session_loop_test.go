package floor_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

// startLoopSession builds a floor with one fake agent @echo (activation
// "mention") and starts its default session with a subscriber. The
// session is closed at test cleanup.
func startLoopSession(t *testing.T) (*floor.Session, <-chan floor.TaggedEvent) {
	t.Helper()
	bp := &blueprint.Blueprint{
		Name:   "test",
		Agents: []blueprint.Agent{{ID: "@echo", Activation: "mention", ToolContext: "full"}},
	}
	f := floor.NewFloor(bp)
	f.AgentFactory = testFactory(map[string]floor.Agent{
		"@echo": &testAgent{id: "@echo", response: func(floor.AgentTurn) string { return "pong" }},
	})
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()
	t.Cleanup(sess.Close)
	return sess, events
}

// collectUntil reads events until one has the type of want, returning
// everything read including it.
func collectUntil[T floor.ChatEvent](t *testing.T, events <-chan floor.TaggedEvent) []floor.ChatEvent {
	t.Helper()
	var got []floor.ChatEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case tagged, ok := <-events:
			if !ok {
				t.Fatalf("subscriber closed; got %v", got)
			}
			got = append(got, tagged.Event)
			if _, ok := tagged.Event.(T); ok {
				return got
			}
		case <-timeout:
			var zero T
			t.Fatalf("timed out waiting for %T; got %v", zero, got)
		}
	}
}

func eventTypes(evs []floor.ChatEvent) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = fmt.Sprintf("%T", ev)
	}
	return out
}

func TestSessionLoopRunsMentionedAgent(t *testing.T) {
	sess, events := startLoopSession(t)
	sess.MainRoom.PostUserInput("@echo? ping")

	got := eventTypes(collectUntil[floor.AwaitingInput](t, events))
	want := []string{"floor.MessagePosted", "floor.AgentStarted", "floor.MessagePosted", "floor.AwaitingInput"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if h := sess.MainRoom.History(); len(h) != 2 || h[1].From != "@echo" {
		t.Errorf("history = %+v", h)
	}
}

func TestSessionLoopClear(t *testing.T) {
	sess, events := startLoopSession(t)
	sess.MainRoom.PostUserInput("@echo? ping")
	collectUntil[floor.AwaitingInput](t, events)

	sess.MainRoom.PostUserInput("/clear")
	got := eventTypes(collectUntil[floor.AwaitingInput](t, events))
	want := []string{"floor.UserCommandEvent", "floor.SessionCleared", "floor.AwaitingInput"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if h := sess.MainRoom.History(); len(h) != 0 {
		t.Errorf("history after /clear = %+v", h)
	}
}

func TestSessionLoopQuitStopsDispatch(t *testing.T) {
	sess, events := startLoopSession(t)
	sess.MainRoom.PostUserInput("/quit")
	collectUntil[floor.SessionStopped](t, events)

	// After /quit the message is still forwarded, but no agent runs.
	sess.MainRoom.PostUserInput("@echo? ping")
	collectUntil[floor.MessagePosted](t, events)
	select {
	case tagged := <-events:
		t.Errorf("unexpected event after /quit: %T", tagged.Event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSessionLoopUnknownAgentReportsInfo(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name:   "test",
		Agents: []blueprint.Agent{{ID: "@ghost", Activation: "mention", ToolContext: "full"}},
	}
	f := floor.NewFloor(bp)
	f.AgentFactory = testFactory(nil) // no instance for @ghost
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()
	defer sess.Close()

	sess.MainRoom.PostUserInput("@ghost? hello")
	got := collectUntil[floor.InfoEvent](t, events)
	if info := got[len(got)-1].(floor.InfoEvent); info.Text == "" {
		t.Errorf("empty info text")
	}
}

func TestSessionSubscriberClosedOnShutdown(t *testing.T) {
	f := floor.NewFloor(&blueprint.Blueprint{Name: "test"})
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()
	sess.Close()
	select {
	case _, ok := <-events:
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber not closed after session close")
	}
}
