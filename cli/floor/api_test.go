package floor

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/furniture"
)

func TestAPIServerMCPEndToEnd(t *testing.T) {
	// Create furniture and wrap as MCP
	tb := furniture.NewTaskBoard()
	mcpSrv := furniture.WrapAsMCP(tb)

	// Start API server on auto-assigned port
	api := NewAPIServer()
	api.RegisterFurniture("default", "tasks", mcpSrv)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start API server: %v", err)
	}
	defer api.Stop()

	baseURL := api.BaseURL()
	if baseURL == "" {
		t.Fatal("BaseURL is empty")
	}
	t.Logf("API server at %s", baseURL)

	// Connect MCP client to the endpoint
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	endpoint := baseURL + "/api/v1/floors/default/mcp/tasks/"
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: endpoint,
	}, nil)
	if err != nil {
		t.Fatalf("failed to connect MCP client: %v", err)
	}
	defer session.Close()

	// List tools
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools.Tools))
	}
	t.Logf("Tools: %v", toolNames(tools.Tools))

	// Add a task
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "add_task",
		Arguments: map[string]any{"title": "Test task", "description": "A test task"},
	})
	if err != nil {
		t.Fatalf("add_task: %v", err)
	}
	if result.IsError {
		t.Fatalf("add_task returned error: %v", contentText(result))
	}
	t.Logf("add_task result: %s", contentText(result))

	// List tasks
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}
	t.Logf("list_tasks result: %s", contentText(result))

	// Update task
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "update_task",
		Arguments: map[string]any{"id": 1, "status": "done"},
	})
	if err != nil {
		t.Fatalf("update_task: %v", err)
	}
	t.Logf("update_task result: %s", contentText(result))

	// Get task
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_task",
		Arguments: map[string]any{"id": 1},
	})
	if err != nil {
		t.Fatalf("get_task: %v", err)
	}
	t.Logf("get_task result: %s", contentText(result))
}

func TestPostMessage(t *testing.T) {
	chat := NewChat()
	api := NewAPIServer()
	api.RegisterFloorAPI(chat)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// POST a message
	resp, err := http.Post(
		api.BaseURL()+"/api/v1/messages",
		"application/json",
		strings.NewReader(`{"from": "@test", "content": "hello from API"}`),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Drain the event channel (Post emits MessagePosted)
	<-chat.Events()

	// Verify message in history
	history := chat.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if history[0].From != "@test" || history[0].Content != "hello from API" {
		t.Fatalf("unexpected message: %+v", history[0])
	}
}

func TestPostMessageDefaultsFrom(t *testing.T) {
	chat := NewChat()
	api := NewAPIServer()
	api.RegisterFloorAPI(chat)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	resp, err := http.Post(
		api.BaseURL()+"/api/v1/messages",
		"application/json",
		strings.NewReader(`{"content": "no from field"}`),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	<-chat.Events()

	history := chat.History()
	if history[0].From != "@user" {
		t.Fatalf("expected @user default, got %q", history[0].From)
	}
}

func TestPostMessageRejectsEmpty(t *testing.T) {
	chat := NewChat()
	api := NewAPIServer()
	api.RegisterFloorAPI(chat)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	resp, err := http.Post(
		api.BaseURL()+"/api/v1/messages",
		"application/json",
		strings.NewReader(`{"from": "@user"}`),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetMessages(t *testing.T) {
	chat := NewChat()
	api := NewAPIServer()
	api.RegisterFloorAPI(chat)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Add messages directly
	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events()
	chat.Post(ChatMessage{From: "@data", Content: "hi back"})
	<-chat.Events()

	// GET messages
	resp, err := http.Get(api.BaseURL() + "/api/v1/messages")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Messages []struct {
			From    string `json:"from"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].From != "@user" || result.Messages[1].Content != "hi back" {
		t.Fatalf("unexpected messages: %+v", result.Messages)
	}
}

func TestSSEEvents(t *testing.T) {
	chat := NewChat()
	api := NewAPIServer()
	api.RegisterFloorAPI(chat)
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Subscribe via SSE
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", api.BaseURL()+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", resp.Header.Get("Content-Type"))
	}

	// Give the SSE handler time to subscribe before posting
	time.Sleep(50 * time.Millisecond)

	// Post a message (this goes to main channel + subscriber)
	go func() {
		chat.Post(ChatMessage{From: "@user", Content: "sse test"})
	}()

	// Drain the main event channel so Post doesn't block
	go func() {
		for range chat.Events() {
		}
	}()

	// Read SSE event
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("SSE read failed: %v", err)
	}
	line := string(buf[:n])
	if !strings.Contains(line, "message_posted") {
		t.Fatalf("expected message_posted event, got: %s", line)
	}
	if !strings.Contains(line, "sse test") {
		t.Fatalf("expected content in event, got: %s", line)
	}
}

func toolNames(tools []*mcp.Tool) []string {
	var names []string
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}

func contentText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return "<no content>"
	}
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "<non-text content>"
}
