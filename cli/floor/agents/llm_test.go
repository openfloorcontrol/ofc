package agents_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/llm/llmtest"
)

// runAgent wires a floor with a single LLM agent pointed at a scripted mock,
// runs one turn, and returns the mock for inspection.
func runAgent(t *testing.T, prompt string, script ...llmtest.Response) *llmtest.Mock {
	t.Helper()

	mock := llmtest.New().Script(script...)
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{{
			ID:          "@a",
			Type:        "llm",
			Endpoint:    srv.URL + "/v1",
			Model:       "test-model",
			Prompt:      "You are @a.",
			Temperature: 0.7,
			Activation:  "always",
			ToolContext: "full",
			Furniture:   []string{"tasks"},
		}},
	}

	f := floor.NewFloor(bp)
	f.Furniture["tasks"] = furniture.NewTaskBoard()

	sess := f.DefaultSession()
	go func() {
		for range sess.MainRoom.Events() {
		}
	}()

	sess.MainRoom.PostUserInput(prompt)

	turn := floor.NewAgentTurn(sess, sess.MainRoom, f, "@a")
	if err := agents.NewLLM(&bp.Agents[0]).Run(context.Background(), turn); err != nil {
		t.Fatalf("agent run: %v", err)
	}
	return mock
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
	mock := runAgent(t, "add two tasks",
		llmtest.Response{
			Content: "Let me add both tasks.",
			ToolCalls: []llmtest.Call{
				{Name: "tasks__add_task", Args: `{"title":"first"}`},
				{Name: "tasks__add_task", Args: `{"title":"second"}`},
			},
		},
		llmtest.Response{Content: "Both added."},
	)

	reqs := mock.Requests()
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
	mock := runAgent(t, "add a task",
		llmtest.Response{
			Content:   "Adding.",
			ToolCalls: []llmtest.Call{{Name: "tasks__add_task", Args: `{"title":"only"}`}},
		},
		llmtest.Response{Content: "Added."},
	)

	msgs := mock.Requests()[1].Messages
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
	mock := runAgent(t, "say hi", llmtest.Response{Content: "Hello."})

	if got := len(mock.Requests()); got != 1 {
		t.Fatalf("got %d requests, want 1", got)
	}
	msgs := mock.Requests()[0].Messages
	if len(byRole(msgs, "system")) != 1 {
		t.Errorf("want the agent's system prompt in the first request: %+v", msgs)
	}
}
