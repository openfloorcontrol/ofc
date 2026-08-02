package agents_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/llm/llmtest"
)

// agentRun is what one scripted turn produced: the requests the agent sent,
// and the messages it posted.
type agentRun struct {
	mock     *llmtest.Mock
	messages []floor.ChatMessage
}

// spoke returns the messages the agent under test posted.
func (r *agentRun) spoke() []floor.ChatMessage {
	var out []floor.ChatMessage
	for _, m := range r.messages {
		if m.From == "@a" {
			out = append(out, m)
		}
	}
	return out
}

// harness is a floor with one scripted LLM agent, reusable across turns so
// tests can check what a later turn replays from stored history.
type harness struct {
	mock  *llmtest.Mock
	floor *floor.Floor
	sess  *floor.Session
	bp    *blueprint.Blueprint
}

func newHarness(t *testing.T, tweak func(*blueprint.Agent), script ...llmtest.Response) *harness {
	t.Helper()

	mock := llmtest.New().Script(script...)
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	agent := blueprint.Agent{
		ID:          "@a",
		Type:        "llm",
		Endpoint:    srv.URL + "/v1",
		Model:       "test-model",
		Prompt:      "You are @a.",
		Temperature: 0.7,
		Activation:  "always",
		ToolContext: "full",
		Furniture:   []string{"tasks"},
	}
	if tweak != nil {
		tweak(&agent)
	}
	bp := &blueprint.Blueprint{Name: "test", Agents: []blueprint.Agent{agent}}

	f := floor.NewFloor(bp)
	f.Furniture["tasks"] = furniture.NewTaskBoard()

	sess := f.DefaultSession()
	go func() {
		for range sess.MainRoom.Events() {
		}
	}()

	return &harness{mock: mock, floor: f, sess: sess, bp: bp}
}

// turn posts a user message and runs one agent turn against it.
func (h *harness) turn(t *testing.T, prompt string) {
	t.Helper()
	h.sess.MainRoom.PostUserInput(prompt)
	turn := floor.NewAgentTurn(h.sess, h.sess.MainRoom, h.floor, "@a")
	if err := agents.NewLLM(&h.bp.Agents[0]).Run(context.Background(), turn); err != nil {
		t.Fatalf("agent run: %v", err)
	}
}

func (h *harness) result() *agentRun {
	return &agentRun{mock: h.mock, messages: h.sess.MainRoom.History()}
}

// runAgent wires a floor with a single LLM agent pointed at a scripted mock
// and runs one turn.
func runAgent(t *testing.T, prompt string, script ...llmtest.Response) *agentRun {
	t.Helper()
	return runAgentWith(t, nil, prompt, script...)
}

// runAgentWith is runAgent with a chance to adjust the agent's blueprint
// before it runs.
func runAgentWith(t *testing.T, tweak func(*blueprint.Agent), prompt string, script ...llmtest.Response) *agentRun {
	t.Helper()
	h := newHarness(t, tweak, script...)
	h.turn(t, prompt)
	return h.result()
}

// byRole splits a recorded message history by role, preserving order.
func byRole(msgs []llmtest.Message, role string) []llmtest.Message {
	var out []llmtest.Message
	for _, m := range msgs {
		if m.Role == role {
			out = append(out, m)
		}
	}
	return out
}

// When a model answers with text plus several tool calls, the history sent
// back on the next iteration must mirror that: one assistant message holding
// the text and every call, then one tool message per result.
func TestToolCallHistoryShape(t *testing.T) {
	run := runAgent(t, "add two tasks",
		llmtest.Response{
			Content: "Let me add both tasks.",
			ToolCalls: []llmtest.Call{
				{Name: "tasks__add_task", Args: `{"title":"first"}`},
				{Name: "tasks__add_task", Args: `{"title":"second"}`},
			},
		},
		llmtest.Response{Content: "Both added."},
	)

	reqs := run.mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2 (initial call + one after the tools ran)", len(reqs))
	}

	msgs := reqs[1].Messages
	assistants := byRole(msgs, "assistant")
	tools := byRole(msgs, "tool")

	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1 carrying both calls: %+v", len(assistants), msgs)
	}
	got := assistants[0]

	if got.Content != "Let me add both tasks." {
		t.Errorf("assistant content = %q, want the model's own text", got.Content)
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("assistant carries %d tool calls, want both", len(got.ToolCalls))
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tool messages, want 2", len(tools))
	}
	for i, tm := range tools {
		if tm.ToolCallID != got.ToolCalls[i].ID {
			t.Errorf("tool message %d has id %q, want %q", i, tm.ToolCallID, got.ToolCalls[i].ID)
		}
		if tm.Content == "" {
			t.Errorf("tool message %d has no output", i)
		}
	}
}

// The tool results must follow the assistant message that requested them.
func TestToolMessagesFollowAssistant(t *testing.T) {
	run := runAgent(t, "add a task",
		llmtest.Response{
			Content:   "Adding.",
			ToolCalls: []llmtest.Call{{Name: "tasks__add_task", Args: `{"title":"only"}`}},
		},
		llmtest.Response{Content: "Added."},
	)

	msgs := run.mock.Requests()[1].Messages
	assistantAt := -1
	for i, m := range msgs {
		switch m.Role {
		case "assistant":
			assistantAt = i
		case "tool":
			if assistantAt == -1 || i < assistantAt {
				t.Fatalf("tool message at %d precedes its assistant message: %+v", i, msgs)
			}
		}
	}
	if assistantAt == -1 {
		t.Fatalf("no assistant message in history: %+v", msgs)
	}
}

// A plain answer with no tool calls is one request and one posted message.
func TestNoToolCallsIsASingleRequest(t *testing.T) {
	run := runAgent(t, "say hi", llmtest.Response{Content: "Hello."})

	if got := len(run.mock.Requests()); got != 1 {
		t.Fatalf("got %d requests, want 1", got)
	}
	msgs := run.mock.Requests()[0].Messages
	if len(byRole(msgs, "system")) != 1 {
		t.Errorf("want the agent's system prompt in the first request: %+v", msgs)
	}
}

// --- Reasoning ---

// The load-bearing assertion: reasoning must never reach the provider on a
// later iteration, not even the same agent's own.
func TestReasoningNeverEntersContext(t *testing.T) {
	run := runAgent(t, "add a task",
		llmtest.Response{
			Content:   "<think>SECRET_REASONING</think>Adding it now.",
			ToolCalls: []llmtest.Call{{Name: "tasks__add_task", Args: `{"title":"x"}`}},
		},
		llmtest.Response{Content: "Done."},
	)

	for i, req := range run.mock.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "SECRET_REASONING") {
				t.Errorf("request %d carries reasoning in a %s message: %q", i, m.Role, m.Content)
			}
			if strings.Contains(m.Content, "<think>") {
				t.Errorf("request %d carries a think tag in a %s message: %q", i, m.Role, m.Content)
			}
		}
	}
}

func TestReasoningStoredOnMessageInlineTags(t *testing.T) {
	run := runAgent(t, "say hi", llmtest.Response{Content: "<think>be brief</think>Hello."})

	spoke := run.spoke()
	if len(spoke) != 1 {
		t.Fatalf("agent posted %d messages, want 1", len(spoke))
	}
	if spoke[0].Content != "Hello." {
		t.Errorf("content = %q, want the answer only", spoke[0].Content)
	}
	if spoke[0].Reasoning != "be brief" {
		t.Errorf("reasoning = %q, want the think block body", spoke[0].Reasoning)
	}
}

func TestReasoningStoredOnMessageDeltaField(t *testing.T) {
	run := runAgent(t, "say hi", llmtest.Response{Reasoning: "keep it short", Content: "Hello."})

	spoke := run.spoke()
	if len(spoke) != 1 {
		t.Fatalf("agent posted %d messages, want 1", len(spoke))
	}
	if spoke[0].Content != "Hello." || spoke[0].Reasoning != "keep it short" {
		t.Errorf("got content=%q reasoning=%q", spoke[0].Content, spoke[0].Reasoning)
	}
}

// [PASS] inside a think block is the model deliberating, not passing. Before
// reasoning was split out, this silently dropped the agent's turn.
func TestPassInsideThinkingIsNotAPass(t *testing.T) {
	run := runAgent(t, "anything",
		llmtest.Response{Content: "<think>Should I answer or is this a [PASS]?</think>Here's my answer."})

	spoke := run.spoke()
	if len(spoke) != 1 {
		t.Fatalf("agent posted %d messages, want 1 — a [PASS] inside thinking swallowed the turn", len(spoke))
	}
	if spoke[0].Content != "Here's my answer." {
		t.Errorf("content = %q, want the answer", spoke[0].Content)
	}
}

// A real [PASS] in the answer still passes.
func TestPassInAnswerStillPasses(t *testing.T) {
	run := runAgent(t, "anything",
		llmtest.Response{Content: "<think>nothing to add</think>[PASS]"})

	if got := len(run.spoke()); got != 0 {
		t.Errorf("agent posted %d messages, want 0 for a genuine [PASS]", got)
	}
}

// thinking: none leaves the tags in place for models where they are content.
func TestThinkingModeNoneKeepsTags(t *testing.T) {
	run := runAgentWith(t, func(a *blueprint.Agent) { a.Thinking = "none" },
		"say hi", llmtest.Response{Content: "<think>kept</think>Hello."})

	spoke := run.spoke()
	if len(spoke) != 1 {
		t.Fatalf("agent posted %d messages, want 1", len(spoke))
	}
	if spoke[0].Content != "<think>kept</think>Hello." {
		t.Errorf("content = %q, want the tags left alone", spoke[0].Content)
	}
	if spoke[0].Reasoning != "" {
		t.Errorf("reasoning = %q, want none extracted", spoke[0].Reasoning)
	}
}

// Custom tag pairs come from the blueprint.
func TestCustomThinkingTags(t *testing.T) {
	run := runAgentWith(t, func(a *blueprint.Agent) {
		a.ThinkingTags = []string{"◁think▷", "◁/think▷"}
	}, "say hi", llmtest.Response{Content: "◁think▷hmm◁/think▷Hello."})

	spoke := run.spoke()
	if len(spoke) != 1 {
		t.Fatalf("agent posted %d messages, want 1", len(spoke))
	}
	if spoke[0].Content != "Hello." || spoke[0].Reasoning != "hmm" {
		t.Errorf("got content=%q reasoning=%q", spoke[0].Content, spoke[0].Reasoning)
	}
}

// The cross-turn path: a stored message carries Reasoning, and buildContext
// must not replay it to the agent that produced it.
func TestReasoningNotReplayedOnLaterTurn(t *testing.T) {
	h := newHarness(t, nil,
		llmtest.Response{Content: "<think>SECRET_REASONING</think>First answer."},
		llmtest.Response{Content: "Second answer."},
	)

	h.turn(t, "first question")
	h.turn(t, "second question")

	reqs := h.mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want one per turn", len(reqs))
	}

	// The second turn rebuilds context from stored history, which is where
	// the first turn's reasoning would leak.
	for _, m := range reqs[1].Messages {
		if strings.Contains(m.Content, "SECRET_REASONING") {
			t.Errorf("turn 2 replays turn 1's reasoning in a %s message: %q", m.Role, m.Content)
		}
	}

	var sawFirstAnswer bool
	for _, m := range reqs[1].Messages {
		if strings.Contains(m.Content, "First answer.") {
			sawFirstAnswer = true
		}
	}
	if !sawFirstAnswer {
		t.Errorf("turn 2 lost the first answer entirely: %+v", reqs[1].Messages)
	}
}
