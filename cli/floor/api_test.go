package floor

import (
	"context"
	"testing"

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
