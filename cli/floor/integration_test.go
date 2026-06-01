package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// --- Test infrastructure ---

// testAgent is a fake agent with a configurable response function.
type testAgent struct {
	id       string
	response func(sess *Session) string // return "" to pass
}

func (a *testAgent) AgentID() string { return a.id }

func (a *testAgent) Run(ctx context.Context, sess *Session) error {
	resp := a.response(sess)
	if resp == "" {
		sess.MainRoom.PostEvent(AgentPassedEvent{AgentID: a.id})
		return nil
	}
	sess.MainRoom.Post(ChatMessage{From: a.id, Content: resp})
	return nil
}

// testLoop is a minimal event loop — the core of CLIFrontend.RunLoop
// without terminal I/O. Runs until ctx is cancelled.
func testLoop(ctx context.Context, sess *Session, ctrl *Controller, agents map[string]Agent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sess.MainRoom.Events():
			if !ok {
				return
			}
			var decision Decision
			switch e := ev.(type) {
			case MessagePosted:
				decision = ctrl.Decide(sess.MainRoom, e)
			case AgentPassedEvent:
				decision = ctrl.Decide(sess.MainRoom, e)
			case AgentErrorEvent:
				decision = ctrl.Decide(sess.MainRoom, e)
			default:
				continue
			}
			if decision.Action == "trigger" {
				agent, ok := agents[decision.AgentID]
				if ok {
					go func() {
						_ = agent.Run(ctx, sess)
					}()
				}
			}
		}
	}
}

// setupTestFloor creates a Floor with API server and event loop running.
// Returns the API base URL and a cleanup function.
func setupTestFloor(t *testing.T, bp *blueprint.Blueprint, agents map[string]Agent) (string, func()) {
	t.Helper()

	floor := NewFloor(bp)
	sess := floor.DefaultSession()

	api := NewAPIServer()
	api.RegisterFloorAPI(sess.MainRoom, bp, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start API server: %v", err)
	}
	floor.APIServer = api

	ctrl := NewController(floor)
	ctx, cancel := context.WithCancel(context.Background())
	go testLoop(ctx, sess, ctrl, agents)

	cleanup := func() {
		cancel()
		api.Stop()
		sess.Close()
	}

	return api.BaseURL(), cleanup
}

// pollMessages polls GET /api/v1/messages until the expected message count
// is reached or the timeout expires.
func pollMessages(t *testing.T, baseURL string, expectedCount int, timeout time.Duration) []struct {
	From    string `json:"from"`
	Content string `json:"content"`
} {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/v1/messages")
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}

		var result struct {
			Messages []struct {
				From    string `json:"from"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if len(result.Messages) >= expectedCount {
			return result.Messages
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d messages", expectedCount)
	return nil
}

func postMessage(t *testing.T, baseURL string, from, content string) {
	t.Helper()
	body := fmt.Sprintf(`{"from": %q, "content": %q}`, from, content)
	resp, err := http.Post(
		baseURL+"/api/v1/messages",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST returned %d", resp.StatusCode)
	}
}

// --- Integration tests ---

func TestIntegrationPostMessageTriggersAgent(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@echo", Activation: "always", ToolContext: "full"},
		},
	}
	agents := map[string]Agent{
		"@echo": &testAgent{
			id: "@echo",
			response: func(sess *Session) string {
				history := sess.MainRoom.History()
				last := history[len(history)-1]
				return "echo: " + last.Content
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	postMessage(t, baseURL, "@user", "hello")

	msgs := pollMessages(t, baseURL, 2, 2*time.Second)
	if msgs[0].From != "@user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].From != "@echo" || msgs[1].Content != "echo: hello" {
		t.Errorf("unexpected agent response: %+v", msgs[1])
	}
}

func TestIntegrationMentionDelegation(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@lead", Activation: "always", ToolContext: "full"},
			{ID: "@helper", Activation: "mention", ToolContext: "full"},
		},
	}

	leadTurns := 0
	agents := map[string]Agent{
		"@lead": &testAgent{
			id: "@lead",
			response: func(sess *Session) string {
				leadTurns++
				if leadTurns == 1 {
					return "asking @helper? for help"
				}
				return "all done, thanks"
			},
		},
		"@helper": &testAgent{
			id: "@helper",
			response: func(sess *Session) string {
				return "helped!"
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	postMessage(t, baseURL, "@user", "start")

	// Expect: @user → @lead (mentions @helper?) → @helper → @lead (done)
	msgs := pollMessages(t, baseURL, 4, 2*time.Second)
	if msgs[0].From != "@user" {
		t.Errorf("msg 0: expected @user, got %s", msgs[0].From)
	}
	if msgs[1].From != "@lead" || !strings.Contains(msgs[1].Content, "@helper?") {
		t.Errorf("msg 1: expected @lead mentioning @helper?, got %+v", msgs[1])
	}
	if msgs[2].From != "@helper" || msgs[2].Content != "helped!" {
		t.Errorf("msg 2: expected @helper, got %+v", msgs[2])
	}
	if msgs[3].From != "@lead" || msgs[3].Content != "all done, thanks" {
		t.Errorf("msg 3: expected @lead done, got %+v", msgs[3])
	}
}

func TestIntegrationAgentPass(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@a", Activation: "always", ToolContext: "full"},
			{ID: "@b", Activation: "always", ToolContext: "full"},
		},
	}
	agents := map[string]Agent{
		"@a": &testAgent{
			id:       "@a",
			response: func(sess *Session) string { return "" }, // pass
		},
		"@b": &testAgent{
			id: "@b",
			response: func(sess *Session) string {
				return "I'm here"
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	postMessage(t, baseURL, "@user", "anyone there?")

	msgs := pollMessages(t, baseURL, 2, 2*time.Second)
	if msgs[1].From != "@b" || msgs[1].Content != "I'm here" {
		t.Errorf("expected @b response, got %+v", msgs[1])
	}
}

func TestIntegrationSSEStream(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@bot", Activation: "always", ToolContext: "full"},
		},
	}
	agents := map[string]Agent{
		"@bot": &testAgent{
			id: "@bot",
			response: func(sess *Session) string {
				return "bot reply"
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	// Subscribe to SSE
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	defer resp.Body.Close()

	// Post a message
	postMessage(t, baseURL, "@user", "hi bot")

	// Read SSE data — collect events until we see the agent's message_posted
	buf := make([]byte, 8192)
	var collected string
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected += string(buf[:n])
		}
		if strings.Contains(collected, "bot reply") {
			break
		}
		if err != nil {
			t.Fatalf("SSE read error: %v (collected so far: %s)", err, collected)
		}
	}

	if !strings.Contains(collected, "message_posted") {
		t.Errorf("expected message_posted event in SSE stream")
	}
	if !strings.Contains(collected, "bot reply") {
		t.Errorf("expected bot reply in SSE stream")
	}
}

func TestIntegrationWebhookFromExternalAgent(t *testing.T) {
	// An external system posts as a custom agent ID — not @user
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@bot", Activation: "always", ToolContext: "full"},
		},
	}
	agents := map[string]Agent{
		"@bot": &testAgent{
			id: "@bot",
			response: func(sess *Session) string {
				history := sess.MainRoom.History()
				last := history[len(history)-1]
				return fmt.Sprintf("got message from %s: %s", last.From, last.Content)
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	// Post as @external — simulating an external system
	postMessage(t, baseURL, "@external", "webhook payload")

	msgs := pollMessages(t, baseURL, 2, 2*time.Second)
	if msgs[0].From != "@external" {
		t.Errorf("expected @external, got %s", msgs[0].From)
	}
	if !strings.Contains(msgs[1].Content, "@external") {
		t.Errorf("agent should see the external sender: %s", msgs[1].Content)
	}
}

func TestIntegrationHistoryEmpty(t *testing.T) {
	bp := &blueprint.Blueprint{Name: "test"}
	baseURL, cleanup := setupTestFloor(t, bp, nil)
	defer cleanup()

	resp, err := http.Get(baseURL + "/api/v1/messages")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Messages []json.RawMessage `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Messages) != 0 {
		t.Errorf("expected empty history, got %d messages", len(result.Messages))
	}
}

func TestIntegrationHistoryPreservesOrder(t *testing.T) {
	// No agents — testLoop drains events, messages just accumulate in history
	bp := &blueprint.Blueprint{Name: "test"}
	baseURL, cleanup := setupTestFloor(t, bp, nil)
	defer cleanup()

	// Post several messages rapidly
	for i := 0; i < 5; i++ {
		postMessage(t, baseURL, "@user", fmt.Sprintf("msg-%d", i))
	}

	msgs := pollMessages(t, baseURL, 5, 2*time.Second)
	for i, m := range msgs {
		expected := fmt.Sprintf("msg-%d", i)
		if m.Content != expected {
			t.Errorf("msg %d: expected %q, got %q", i, expected, m.Content)
		}
	}
}

func TestIntegrationSSEMultipleSubscribers(t *testing.T) {
	bp := &blueprint.Blueprint{Name: "test"}
	baseURL, cleanup := setupTestFloor(t, bp, nil)
	defer cleanup()

	// Connect two SSE subscribers
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req1, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/events", nil)
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("SSE 1 connect failed: %v", err)
	}
	defer resp1.Body.Close()

	req2, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/events", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("SSE 2 connect failed: %v", err)
	}
	defer resp2.Body.Close()

	// Small delay to ensure both SSE handlers have called Subscribe()
	time.Sleep(50 * time.Millisecond)

	// Post a message
	postMessage(t, baseURL, "@user", "broadcast")

	// Both subscribers should receive the event
	for i, resp := range []*http.Response{resp1, resp2} {
		buf := make([]byte, 4096)
		n, err := resp.Body.Read(buf)
		if err != nil {
			t.Fatalf("SSE %d read error: %v", i+1, err)
		}
		data := string(buf[:n])
		if !strings.Contains(data, "broadcast") {
			t.Errorf("SSE subscriber %d didn't receive message: %s", i+1, data)
		}
	}
}

func TestIntegrationWebhookBadRequest(t *testing.T) {
	bp := &blueprint.Blueprint{Name: "test"}
	baseURL, cleanup := setupTestFloor(t, bp, nil)
	defer cleanup()

	// Empty content
	resp, _ := http.Post(baseURL+"/api/v1/messages", "application/json",
		strings.NewReader(`{"from": "@user", "content": ""}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty content: expected 400, got %d", resp.StatusCode)
	}

	// Invalid JSON
	resp, _ = http.Post(baseURL+"/api/v1/messages", "application/json",
		strings.NewReader(`not json`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: expected 400, got %d", resp.StatusCode)
	}

	// Missing content field
	resp, _ = http.Post(baseURL+"/api/v1/messages", "application/json",
		strings.NewReader(`{"from": "@user"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing content: expected 400, got %d", resp.StatusCode)
	}
}

func TestIntegrationMultipleMessages(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@echo", Activation: "always", ToolContext: "full"},
		},
	}
	agents := map[string]Agent{
		"@echo": &testAgent{
			id: "@echo",
			response: func(sess *Session) string {
				history := sess.MainRoom.History()
				last := history[len(history)-1]
				return "echo: " + last.Content
			},
		},
	}

	baseURL, cleanup := setupTestFloor(t, bp, agents)
	defer cleanup()

	// First message
	postMessage(t, baseURL, "@user", "first")
	pollMessages(t, baseURL, 2, 2*time.Second)

	// Second message
	postMessage(t, baseURL, "@user", "second")
	msgs := pollMessages(t, baseURL, 4, 2*time.Second)

	if msgs[2].Content != "second" {
		t.Errorf("expected 'second', got %q", msgs[2].Content)
	}
	if msgs[3].Content != "echo: second" {
		t.Errorf("expected 'echo: second', got %q", msgs[3].Content)
	}
}
