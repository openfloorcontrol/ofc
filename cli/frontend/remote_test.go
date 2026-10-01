package frontend_test

import (
	"context"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/frontend"
)

// echoAgent replies "pong" to every turn.
type echoAgent struct{ id string }

func (a *echoAgent) AgentID() string { return a.id }
func (a *echoAgent) Run(ctx context.Context, turn floor.AgentTurn) error {
	turn.Reply(floor.ChatMessage{From: a.id, Content: "pong"})
	return nil
}

// startServer serves a floor with @echo over HTTP with the given token.
func startServer(t *testing.T, token string) (*floor.Floor, string) {
	t.Helper()
	bp := &blueprint.Blueprint{
		Name:   "remote-test",
		Agents: []blueprint.Agent{{ID: "@echo", Activation: "mention", ToolContext: "full"}},
	}
	f := floor.NewFloor(bp)
	f.AgentFactory = func(*blueprint.Agent) floor.Agent { return &echoAgent{id: "@echo"} }
	f.SessionMetaTemplate = &floor.SessionMeta{BlueprintName: bp.Name}
	srv := api.New()
	srv.SetAuthToken(token)
	f.APIServer = srv
	srv.RegisterFloorAPI(f)
	if err := srv.Start(":0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	return f, srv.BaseURL()
}

// waitFor reads events until one of type T arrives, returning all read.
func waitFor[T floor.ChatEvent](t *testing.T, events <-chan floor.TaggedEvent) []floor.ChatEvent {
	t.Helper()
	var got []floor.ChatEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case tagged, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed; got %v", got)
			}
			got = append(got, tagged.Event)
			if _, ok := tagged.Event.(T); ok {
				return got
			}
		case <-timeout:
			t.Fatalf("timed out; got %v", got)
		}
	}
}

func TestRemoteSessionNewAndAttach(t *testing.T) {
	_, base := startServer(t, "secret")

	client, err := frontend.NewRemoteSession(base, "secret", "")
	if err != nil {
		t.Fatalf("new remote session: %v", err)
	}
	if info := client.Info(); info.Name != "remote-test" || len(info.Agents) != 1 {
		t.Errorf("info = %+v", info)
	}
	if err := client.PostUserInput("@echo? ping"); err != nil {
		t.Fatal(err)
	}
	var replied bool
	for _, ev := range waitFor[floor.AwaitingInput](t, client.Events()) {
		if mp, ok := ev.(floor.MessagePosted); ok && mp.Message.From == "@echo" && mp.Message.Content == "pong" {
			replied = true
		}
	}
	if !replied {
		t.Error("no reply from @echo over the remote stream")
	}
	client.Close()

	// A second client attaches to the same session and sees its history.
	attached, err := frontend.NewRemoteSession(base, "secret", client.ID())
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer attached.Close()
	if h := attached.History(); len(h) != 2 || h[1].Content != "pong" {
		t.Errorf("history = %+v", h)
	}
}

func TestRemoteSessionNeedsToken(t *testing.T) {
	_, base := startServer(t, "secret")
	if _, err := frontend.NewRemoteSession(base, "wrong", ""); err == nil {
		t.Error("expected an error with a wrong token")
	}
}
