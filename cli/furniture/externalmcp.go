package furniture

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExternalMCP implements the Furniture interface by proxying to an external
// MCP server subprocess via stdio. OFC spawns the process, connects as an
// MCP client, discovers tools, and forwards Call() invocations.
type ExternalMCP struct {
	name    string
	session *mcp.ClientSession
	tools   []Tool // cached from tools/list at startup
}

// NewExternalMCP spawns an external MCP server process and connects to it.
// It performs the MCP handshake and discovers available tools.
func NewExternalMCP(ctx context.Context, name, command string, args []string) (*ExternalMCP, error) {
	cmd := exec.Command(command, args...)
	transport := &mcp.CommandTransport{Command: cmd}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "ofc",
		Version: "0.1.0",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server %q (%s): %w", name, command, err)
	}

	// Discover tools
	var tools []Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			session.Close()
			return nil, fmt.Errorf("list tools for MCP server %q: %w", name, err)
		}
		tools = append(tools, convertMCPTool(tool))
	}

	return &ExternalMCP{
		name:    name,
		session: session,
		tools:   tools,
	}, nil
}

func (e *ExternalMCP) Name() string  { return e.name }
func (e *ExternalMCP) Tools() []Tool { return e.tools }

// Call proxies a tool invocation to the external MCP server.
func (e *ExternalMCP) Call(toolName string, args map[string]interface{}) (interface{}, error) {
	result, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("call tool %q on %q: %w", toolName, e.name, err)
	}

	if result.IsError {
		return nil, fmt.Errorf("tool %q error: %s", toolName, extractTextContent(result.Content))
	}

	return extractTextContent(result.Content), nil
}

// Close shuts down the MCP session and kills the subprocess.
func (e *ExternalMCP) Close() error {
	if e.session != nil {
		return e.session.Close()
	}
	return nil
}

// convertMCPTool converts an MCP Tool to our furniture Tool type.
func convertMCPTool(t *mcp.Tool) Tool {
	var params map[string]interface{}
	if t.InputSchema != nil {
		// InputSchema is any — marshal/unmarshal to get a map
		data, err := json.Marshal(t.InputSchema)
		if err == nil {
			json.Unmarshal(data, &params)
		}
	}
	return Tool{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  params,
	}
}

// extractTextContent pulls text from MCP Content blocks.
func extractTextContent(content []mcp.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
