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
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
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
// answers every message. The floor stops at test cleanup.
func startACPFloor(t *testing.T) *floor.Floor {
	t.Helper()
	f := newACPFloor(t, floor.NewMemoryStore(), "", 0)
	t.Cleanup(f.Stop)
	return f
}

// newACPFloor starts a floor with @fake on the given store. stateDir is
// where @fake keeps its sessions for resume ("" for none); idle is the
// ACP idle timeout (0 for none). The caller stops the floor.
func newACPFloor(t *testing.T, store floor.SessionStore, stateDir string, idle time.Duration) *floor.Floor {
	t.Helper()
	bp := &blueprint.Blueprint{
		Name:   "acp-test",
		Config: blueprint.Config{ACPIdleTimeout: idle},
		Agents: []blueprint.Agent{{
			ID: "@fake", Type: "acp", Command: fakeAgent(t), Activation: "always",
			Env:    map[string]string{"FAKE_ACP_STATE_DIR": stateDir},
			Prompt: "You are @fake.",
		}},
	}
	f := floor.NewFloor(bp)
	f.Store = store
	f.AgentFactory = agents.New
	f.APIServer = api.New()
	f.StderrWriter = os.Stderr
	if err := f.Start(func(string) {}); err != nil {
		t.Fatalf("start floor: %v", err)
	}
	return f
}

// A session reopened after a restart resumes the agent's ACP session in
// a new process, which is sent only what it hasn't seen.
func TestACPSessionResumesAfterRestart(t *testing.T) {
	storeDir, stateDir := t.TempDir(), t.TempDir()

	store1, err := sessionstore.NewJSONL(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	f1 := newACPFloor(t, store1, stateDir, 0)
	sess, err := f1.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	events := sess.Subscribe()
	first := ask(t, sess, events, "one")
	if !strings.Contains(first, "prompts=1 ") || !strings.Contains(first, "You are @fake.") {
		t.Fatalf("first reply = %q, want prompt 1 with the system prompt", first)
	}
	ask(t, sess, events, "two")
	f1.Stop()

	store2, err := sessionstore.NewJSONL(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	f2 := newACPFloor(t, store2, stateDir, 0)
	defer f2.Stop()
	resumed, err := f2.Session(sess.ID())
	if err != nil {
		t.Fatalf("resume session: %v", err)
	}
	reply := ask(t, resumed, resumed.Subscribe(), "three")

	if !strings.Contains(reply, "prompts=3 ") {
		t.Errorf("reply = %q, want the agent's third prompt in the same ACP session", reply)
	}
	if pid(t, reply) == pid(t, first) {
		t.Errorf("resumed session reused process %s", pid(t, reply))
	}
	_, text, _ := strings.Cut(reply, " | ")
	if !strings.Contains(text, "three") || strings.Contains(text, "one") || strings.Contains(text, "two") || strings.Contains(text, "You are @fake.") {
		t.Errorf("resumed agent received %q, want only the new message", text)
	}
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

// An idle agent process is closed after the idle timeout; the session's
// next turn resumes the agent's session in a new process.
func TestACPIdleSubprocessClosedAndResumed(t *testing.T) {
	f := newACPFloor(t, floor.NewMemoryStore(), t.TempDir(), 200*time.Millisecond)
	defer f.Stop()
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	first := ask(t, sess, events, "one")
	p := pid(t, first)
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(p) {
		if time.Now().After(deadline) {
			t.Fatalf("idle agent process %s still running", p)
		}
		time.Sleep(20 * time.Millisecond)
	}

	second := ask(t, sess, events, "two")
	if pid(t, second) == p {
		t.Errorf("second turn reused closed process %s", p)
	}
	if !strings.Contains(second, "prompts=2 ") {
		t.Errorf("second reply = %q, want the resumed session's second prompt", second)
	}
}

// A turn that runs longer than the idle timeout keeps its process.
func TestACPBusySubprocessNotClosed(t *testing.T) {
	f := newACPFloor(t, floor.NewMemoryStore(), t.TempDir(), 100*time.Millisecond)
	defer f.Stop()
	sess := f.DefaultSession()
	events := sess.Subscribe()
	sess.Start()

	// The fake agent sleeps for "sleep=<ms>" in its prompt.
	reply := ask(t, sess, events, "sleep=400")
	if !strings.HasPrefix(reply, "pid=") {
		t.Fatalf("reply = %q, want the agent's text", reply)
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
