package furniture

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WrapAsMCP creates an MCP server that exposes the furniture's tools.
// Each furniture tool is registered as an MCP tool with the low-level API.
func WrapAsMCP(f Furniture) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    f.Name(),
		Version: "1.0.0",
	}, nil)

	for _, tool := range f.Tools() {
		toolName := tool.Name
		srv.AddTool(
			&mcp.Tool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.Parameters,
			},
			makeHandler(f, toolName),
		)
	}

	return srv
}

// makeHandler creates a ToolHandler that delegates to the Furniture.Call method.
func makeHandler(f Furniture, toolName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from JSON
		var args map[string]interface{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("invalid arguments: %v", err)},
					},
					IsError: true,
				}, nil
			}
		}
		if args == nil {
			args = make(map[string]interface{})
		}

		// Call the furniture
		result, err := f.Call(toolName, args)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("error: %v", err)},
				},
				IsError: true,
			}, nil
		}

		// Marshal result to JSON text
		data, err := json.Marshal(result)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("failed to marshal result: %v", err)},
				},
				IsError: true,
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil
	}
}
