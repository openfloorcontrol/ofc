package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/furniture"
)

func TestAPIServerMCPEndToEnd(t *testing.T) {
	// Create furniture and wrap as MCP
	tb := furniture.NewTaskBoard()
	mcpSrv := furniture.WrapAsMCP(tb)

	// Start API server on auto-assigned port
	api := api.New()
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
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
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
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
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
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
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
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Add messages directly
	chat.Post(floor.ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events()
	chat.Post(floor.ChatMessage{From: "@data", Content: "hi back"})
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
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
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
		chat.Post(floor.ChatMessage{From: "@user", Content: "sse test"})
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

func TestGetAgents(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name:        "test-floor",
		Description: "A test floor",
		Agents: []blueprint.Agent{
			{ID: "@planner", Name: "Planner", Activation: "always"},
			{ID: "@coder", Name: "Coder", Type: "acp", Activation: "mention"},
		},
	}

	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, bp, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	resp, err := http.Get(api.BaseURL() + "/api/v1/agents")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		FloorName   string `json:"floor_name"`
		Description string `json:"description"`
		Agents      []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Type       string `json:"type"`
			Activation string `json:"activation"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if result.FloorName != "test-floor" {
		t.Fatalf("expected floor_name 'test-floor', got %q", result.FloorName)
	}
	if len(result.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(result.Agents))
	}
	if result.Agents[0].ID != "@planner" || result.Agents[0].Type != "llm" {
		t.Fatalf("unexpected first agent: %+v", result.Agents[0])
	}
	if result.Agents[1].ID != "@coder" || result.Agents[1].Type != "acp" {
		t.Fatalf("unexpected second agent: %+v", result.Agents[1])
	}
}

func TestAuthMiddlewareBlocksWithoutToken(t *testing.T) {
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.SetAuthToken("test-secret-token")
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Request without token → 401
	resp, err := http.Get(api.BaseURL() + "/api/v1/agents")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Request with Bearer header → 200
	req, _ := http.NewRequest("GET", api.BaseURL()+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer test-secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with token failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", resp.StatusCode)
	}

	// Request with query param → 200
	resp, err = http.Get(api.BaseURL() + "/api/v1/agents?token=test-secret-token")
	if err != nil {
		t.Fatalf("GET with query token failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with query token, got %d", resp.StatusCode)
	}
}

func TestFurnitureCallProxy(t *testing.T) {
	tb := furniture.NewTaskBoard()
	furMap := map[string]furniture.Furniture{"tasks": tb}

	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, furMap, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Add a task
	resp, err := http.Post(
		api.BaseURL()+"/api/v1/furniture/tasks/call",
		"application/json",
		strings.NewReader(`{"tool": "add_task", "args": {"title": "Test task"}}`),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// List tasks
	resp, err = http.Post(
		api.BaseURL()+"/api/v1/furniture/tasks/call",
		"application/json",
		strings.NewReader(`{"tool": "list_tasks", "args": {}}`),
	)
	if err != nil {
		t.Fatalf("POST list_tasks failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result interface{} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	t.Logf("list_tasks result: %v", result.Result)

	// Unknown furniture → 404
	resp, err = http.Post(
		api.BaseURL()+"/api/v1/furniture/nonexistent/call",
		"application/json",
		strings.NewReader(`{"tool": "list_tasks", "args": {}}`),
	)
	if err != nil {
		t.Fatalf("POST unknown furniture failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown furniture, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.SetAuthToken("test-token-123")
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	// Unauthenticated request should be rejected
	resp, err := http.Get(api.BaseURL() + "/api/v1/agents")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	// Request with Bearer token should succeed
	req, _ := http.NewRequest("GET", api.BaseURL()+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with token failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", resp.StatusCode)
	}

	// Request with query param token should succeed
	resp, err = http.Get(api.BaseURL() + "/api/v1/agents?token=test-token-123")
	if err != nil {
		t.Fatalf("GET with query token failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with query token, got %d", resp.StatusCode)
	}
}

func TestGetFurniture(t *testing.T) {
	tb := furniture.NewTaskBoard()
	furMap := map[string]furniture.Furniture{"tasks": tb}

	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, furMap, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	resp, err := http.Get(api.BaseURL() + "/api/v1/furniture")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Furniture []struct {
			Name  string `json:"name"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"furniture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(result.Furniture) != 1 {
		t.Fatalf("expected 1 furniture, got %d", len(result.Furniture))
	}
	if result.Furniture[0].Name != "tasks" {
		t.Fatalf("expected name 'tasks', got %q", result.Furniture[0].Name)
	}
	if len(result.Furniture[0].Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(result.Furniture[0].Tools))
	}
}

func TestGetFurnitureEmpty(t *testing.T) {
	chat := floor.NewFloor(&blueprint.Blueprint{Name: "test"}).DefaultSession().MainRoom
	api := api.New()
	api.RegisterFloorAPI(chat, &blueprint.Blueprint{}, nil, func() string { return "" })
	if err := api.Start(":0"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer api.Stop()

	resp, err := http.Get(api.BaseURL() + "/api/v1/furniture")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Furniture []interface{} `json:"furniture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(result.Furniture) != 0 {
		t.Fatalf("expected empty furniture list, got %d", len(result.Furniture))
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
