// Package llmtest provides a scriptable, OpenAI-compatible chat-completions
// server for tests.
//
// Mock is an http.Handler, so the caller owns the server:
//
//	mock := llmtest.New()
//	mock.Script(
//		llmtest.Response{Content: "Checking.", ToolCalls: []llmtest.Call{{Name: "read"}}},
//		llmtest.Response{Content: "Done."},
//	)
//	srv := httptest.NewServer(mock)
//	defer srv.Close()
//
//	// point the client under test at srv.URL, then inspect what it sent:
//	sent := mock.Requests()[1].Messages
//
// Scripted responses are played back in order, one per request. Recording the
// requests is the point: it is what lets a test assert on the message history
// a client builds, which is where agent loops usually go wrong.
//
// The package depends only on the standard library so it can be lifted into
// its own module, and its types mirror the OpenAI wire format rather than any
// particular client's types.
package llmtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Call is one tool call, flattened from the nested wire representation.
type Call struct {
	ID   string
	Name string
	Args string // JSON-encoded arguments object
}

// UnmarshalJSON reads the OpenAI nested form into the flat one.
func (c *Call) UnmarshalJSON(b []byte) error {
	var w struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	c.ID, c.Name, c.Args = w.ID, w.Function.Name, w.Function.Arguments
	return nil
}

// Tool is a tool definition as offered by the client.
type Tool struct {
	Name        string
	Description string
}

func (t *Tool) UnmarshalJSON(b []byte) error {
	var w struct {
		Function struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"function"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	t.Name, t.Description = w.Function.Name, w.Function.Description
	return nil
}

// Message is one entry of a recorded request's message history.
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
}

// Request is a recorded chat-completions request.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
	Tools       []Tool    `json:"tools,omitempty"`

	// Authorization is the raw request header, e.g. "Bearer sk-123".
	Authorization string `json:"-"`
}

// Response is one scripted reply.
type Response struct {
	// Content is the assistant text. It is sent as a single SSE frame
	// unless Chunks is set.
	Content string

	// Chunks splits the assistant text across several frames. Use it to
	// exercise a client's handling of token boundaries; when set, Content
	// is ignored.
	Chunks []string

	// Reasoning is sent as a reasoning_content delta ahead of the text, the
	// way servers that separate thinking server-side do it. Models that
	// inline their thinking instead are scripted through Content/Chunks.
	Reasoning string

	// ToolCalls are emitted after the text, each as its own frame.
	ToolCalls []Call
}

func (r Response) frames() []string {
	if r.Chunks != nil {
		return r.Chunks
	}
	if r.Content == "" {
		return nil
	}
	return []string{r.Content}
}

// Mock is a scriptable chat-completions handler.
type Mock struct {
	mu       sync.Mutex
	script   []Response
	next     int
	requests []Request
}

// New returns an empty Mock. Add replies with Script.
func New() *Mock { return &Mock{} }

// Script appends replies to the queue. Returns the Mock for chaining.
func (m *Mock) Script(rs ...Response) *Mock {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.script = append(m.script, rs...)
	return m
}

// Requests returns the requests received so far, in order.
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, len(m.requests))
	copy(out, m.requests)
	return out
}

func (m *Mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.Error(w, fmt.Sprintf("llmtest: unexpected request %s %s", r.Method, r.URL.Path),
			http.StatusNotFound)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "llmtest: decode request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Authorization = r.Header.Get("Authorization")

	m.mu.Lock()
	m.requests = append(m.requests, req)
	if m.next >= len(m.script) {
		n := len(m.script)
		m.mu.Unlock()
		http.Error(w, fmt.Sprintf("llmtest: script exhausted after %d responses", n),
			http.StatusInternalServerError)
		return
	}
	resp := m.script[m.next]
	m.next++
	m.mu.Unlock()

	writeStream(w, req.Model, resp)
}

// --- SSE emission ---

type wireDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
}

type wireToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireChoice struct {
	Index        int       `json:"index"`
	Delta        wireDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type wireChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
}

func writeStream(w http.ResponseWriter, model string, resp Response) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	send := func(d wireDelta, finish string) {
		choice := wireChoice{Delta: d}
		if finish != "" {
			choice.FinishReason = &finish
		}
		body, err := json.Marshal(wireChunk{
			ID:      "chatcmpl-llmtest",
			Object:  "chat.completion.chunk",
			Model:   model,
			Choices: []wireChoice{choice},
		})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", body)
		if flusher != nil {
			flusher.Flush()
		}
	}

	send(wireDelta{Role: "assistant"}, "")
	if resp.Reasoning != "" {
		send(wireDelta{ReasoningContent: resp.Reasoning}, "")
	}
	for _, text := range resp.frames() {
		send(wireDelta{Content: text}, "")
	}

	for i, c := range resp.ToolCalls {
		var wc wireToolCall
		wc.Index = i
		wc.ID = c.ID
		if wc.ID == "" {
			wc.ID = fmt.Sprintf("call_%d", i)
		}
		wc.Type = "function"
		wc.Function.Name = c.Name
		wc.Function.Arguments = c.Args
		if wc.Function.Arguments == "" {
			wc.Function.Arguments = "{}"
		}
		send(wireDelta{ToolCalls: []wireToolCall{wc}}, "")
	}

	finish := "stop"
	if len(resp.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	send(wireDelta{}, finish)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
