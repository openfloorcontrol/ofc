package llmtest_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/llm"
	"github.com/openfloorcontrol/ofc/llm/llmtest"
)

// newServer wires a scripted mock behind an httptest server and returns a
// client pointed at it.
func newServer(t *testing.T, apiKey string, rs ...llmtest.Response) (*llmtest.Mock, *llm.Client) {
	t.Helper()
	mock := llmtest.New().Script(rs...)
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	return mock, llm.NewClient(srv.URL+"/v1", apiKey)
}

func TestStreamsContent(t *testing.T) {
	mock, client := newServer(t, "", llmtest.Response{Content: "Hello there."})

	var streamed strings.Builder
	result, err := client.ChatStream("test-model",
		[]llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil,
		func(tok string) { streamed.WriteString(tok) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != "Hello there." {
		t.Errorf("content = %q, want %q", result.Content, "Hello there.")
	}
	if streamed.String() != "Hello there." {
		t.Errorf("streamed = %q, want %q", streamed.String(), "Hello there.")
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(got))
	}
	if got := mock.Requests()[0]; got.Model != "test-model" || !got.Stream {
		t.Errorf("request = %+v, want model=test-model stream=true", got)
	}
}

// Chunks let a test drive token boundaries explicitly — the content must
// still reassemble, and the client must see each frame separately.
func TestChunksArriveSeparately(t *testing.T) {
	_, client := newServer(t, "", llmtest.Response{Chunks: []string{"Hel", "lo ", "there."}})

	var tokens []string
	result, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil,
		func(tok string) { tokens = append(tokens, tok) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != "Hello there." {
		t.Errorf("content = %q, want %q", result.Content, "Hello there.")
	}
	if len(tokens) != 3 {
		t.Errorf("got %d tokens %q, want 3", len(tokens), tokens)
	}
}

func TestToolCalls(t *testing.T) {
	_, client := newServer(t, "", llmtest.Response{
		Content: "Looking those up.",
		ToolCalls: []llmtest.Call{
			{Name: "read", Args: `{"path":"a"}`},
			{ID: "custom-id", Name: "read", Args: `{"path":"b"}`},
		},
	})

	result, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(result.ToolCalls))
	}
	if result.Content != "Looking those up." {
		t.Errorf("content = %q, want the text alongside the calls", result.Content)
	}
	if got := result.ToolCalls[0]; got.ID != "call_0" || got.Function.Arguments != `{"path":"a"}` {
		t.Errorf("call 0 = %+v, want generated id and path a", got)
	}
	if got := result.ToolCalls[1]; got.ID != "custom-id" {
		t.Errorf("call 1 id = %q, want the scripted id", got.ID)
	}
}

func TestRecordsAuthorizationAndTools(t *testing.T) {
	mock, client := newServer(t, "sk-test", llmtest.Response{Content: "ok"})

	tool := llm.Tool{Type: "function"}
	tool.Function.Name = "tasks__add_task"
	tool.Function.Description = "Add a task"

	if _, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7,
		[]llm.Tool{tool}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := mock.Requests()[0]
	if req.Authorization != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want %q", req.Authorization, "Bearer sk-test")
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "tasks__add_task" {
		t.Errorf("tools = %+v, want the offered tool", req.Tools)
	}
}

func TestNoAPIKeyMeansNoAuthHeader(t *testing.T) {
	mock, client := newServer(t, "", llmtest.Response{Content: "ok"})

	if _, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := mock.Requests()[0].Authorization; got != "" {
		t.Errorf("Authorization = %q, want it absent", got)
	}
}

// An unscripted call must fail loudly rather than hang or quietly succeed.
func TestScriptExhausted(t *testing.T) {
	_, client := newServer(t, "", llmtest.Response{Content: "only one"})

	if _, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	_, err := client.ChatStream("m", []llm.Message{{Role: "user", Content: "hi"}}, 0.7, nil, nil)
	if err == nil {
		t.Fatal("expected the second call to fail")
	}
	if !strings.Contains(err.Error(), "script exhausted") {
		t.Errorf("error = %v, want it to mention the exhausted script", err)
	}
}
