package agents_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents"
)

var (
	fakeAgentOnce sync.Once
	fakeAgentPath string
	fakeAgentErr  error
)

// fakeAgent builds cli/acp/fakeagent once per test run and returns its path.
func fakeAgent(t *testing.T) string {
	t.Helper()
	fakeAgentOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ofc-fakeagent")
		if err != nil {
			fakeAgentErr = err
			return
		}
		fakeAgentPath = filepath.Join(dir, "fakeagent")
		out, err := exec.Command("go", "build", "-o", fakeAgentPath, "github.com/openfloorcontrol/ofc/acp/fakeagent").CombinedOutput()
		if err != nil {
			fakeAgentErr = err
			t.Logf("go build fakeagent: %s", out)
		}
	})
	if fakeAgentErr != nil {
		t.Fatalf("build fakeagent: %v", fakeAgentErr)
	}
	return fakeAgentPath
}

// startACPFloor starts a floor with one fake ACP agent, @fake, that
// answers every message.
func startACPFloor(t *testing.T) *floor.Floor {
	t.Helper()
	bp := &blueprint.Blueprint{
		Name: "acp-test",
		Agents: []blueprint.Agent{
			{ID: "@fake", Type: "acp", Command: fakeAgent(t), Activation: "always"},
		},
	}
	f := floor.NewFloor(bp)
	f.AgentFactory = agents.New
	f.APIServer = api.New()
	f.StderrWriter = os.Stderr
	if err := f.Start(func(string) {}); err != nil {
		t.Fatalf("start floor: %v", err)
	}
	t.Cleanup(f.Stop)
	return f
}

// ask posts a user message to the session and returns @fake's reply.
func ask(t *testing.T, sess *floor.Session, events <-chan floor.TaggedEvent, text string) string {
	t.Helper()
	sess.MainRoom.PostUserInput(text)
	timeout := time.After(10 * time.Second)
	for {
		select {
		case tagged := <-events:
			switch e := tagged.Event.(type) {
			case floor.MessagePosted:
				if e.Message.From == "@fake" {
					return e.Message.Content
				}
			case floor.AgentErrorEvent:
				t.Fatalf("agent error: %v", e.Err)
			}
		case <-timeout:
			t.Fatalf("no reply to %q", text)
		}
	}
}

// The reply must hold the agent's streamed text even when the agent ends
// its turn right after sending it.
func TestACPReplyHasStreamedText(t *testing.T) {
	f := startACPFloor(t)
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	for i := 0; i < 5; i++ {
		reply := ask(t, sess, events, "hello")
		if !strings.HasPrefix(reply, "pid=") {
			t.Fatalf("turn %d: reply = %q, want the agent's text", i, reply)
		}
	}
}
