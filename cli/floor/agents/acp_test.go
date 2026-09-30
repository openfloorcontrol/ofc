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

// pid extracts the process id from a fakeagent reply.
func pid(t *testing.T, reply string) string {
	t.Helper()
	field, _, ok := strings.Cut(reply, " ")
	if !ok || !strings.HasPrefix(field, "pid=") {
		t.Fatalf("not a fakeagent reply: %q", reply)
	}
	return strings.TrimPrefix(field, "pid=")
}

// Each session talks to its own agent process, and a session keeps its
// process across turns.
func TestACPSubprocessPerSession(t *testing.T) {
	f := startACPFloor(t)
	a, err := f.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	aEvents, bEvents := a.Subscribe(), b.Subscribe()

	a1 := pid(t, ask(t, a, aEvents, "one"))
	b1 := pid(t, ask(t, b, bEvents, "one"))
	a2 := pid(t, ask(t, a, aEvents, "two"))

	if a1 == b1 {
		t.Errorf("sessions share agent process %s", a1)
	}
	if a1 != a2 {
		t.Errorf("session changed agent process between turns: %s, then %s", a1, a2)
	}
}

// /quit ends the session's agent process.
func TestACPSubprocessClosedOnQuit(t *testing.T) {
	f := startACPFloor(t)
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	p := pid(t, ask(t, sess, events, "hello"))
	sess.MainRoom.PostUserInput("/quit")
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(p) {
		if time.Now().After(deadline) {
			t.Fatalf("agent process %s still running after /quit", p)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processAlive reports whether a process with the given pid exists.
func processAlive(pid string) bool {
	_, err := os.Stat("/proc/" + pid)
	return err == nil
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
