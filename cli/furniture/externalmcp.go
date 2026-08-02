package furniture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// headerTransport wraps an http.RoundTripper to inject custom headers.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// ExternalMCP implements the Furniture interface by proxying to an external
// MCP server. Supports two connection modes:
//   - Subprocess via stdio (NewExternalMCP)
//   - Already-running server via HTTP (NewExternalMCPFromURL)
type ExternalMCP struct {
	name    string
	session *mcp.ClientSession
	tools   []Tool // cached from tools/list at startup
}

// newClient creates a shared MCP client instance.
func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{
		Name:    "ofc",
		Version: "0.1.0",
	}, nil)
}

// discoverTools lists tools from a connected session.
func discoverTools(ctx context.Context, session *mcp.ClientSession, name string) ([]Tool, error) {
	var tools []Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list tools for MCP server %q: %w", name, err)
		}
		tools = append(tools, convertMCPTool(tool))
	}
	return tools, nil
}

// NewExternalMCP spawns an external MCP server process and connects via stdio.
// If cwd is non-empty, the subprocess runs in that directory (used to resolve
// relative paths in command/args against the blueprint's location).
func NewExternalMCP(ctx context.Context, name, command string, args []string, cwd string) (*ExternalMCP, error) {
	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	transport := &mcp.CommandTransport{Command: cmd}

	session, err := newClient().Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server %q (%s): %w", name, command, err)
	}

	tools, err := discoverTools(ctx, session, name)
	if err != nil {
		session.Close()
		return nil, err
	}

	return &ExternalMCP{name: name, session: session, tools: tools}, nil
}

// NewExternalMCPFromURL connects to an already-running MCP server via HTTP.
// Headers are added to every request, verbatim — ${VAR} references in
// blueprint values are already resolved by blueprint.Load.
func NewExternalMCPFromURL(ctx context.Context, name, url string, headers map[string]string) (*ExternalMCP, error) {
	transport := &mcp.StreamableClientTransport{Endpoint: url}

	if len(headers) > 0 {
		transport.HTTPClient = &http.Client{
			Transport: &headerTransport{
				base:    http.DefaultTransport,
				headers: headers,
			},
		}
	}

	session, err := newClient().Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server %q (%s): %w", name, url, err)
	}

	tools, err := discoverTools(ctx, session, name)
	if err != nil {
		session.Close()
		return nil, err
	}

	return &ExternalMCP{name: name, session: session, tools: tools}, nil
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

// ReadFileRaw reads a file via the MCP server and returns raw bytes.
// The caller provides the path exactly as the MCP server expects it.
// Uses read_media_file (binary-safe) if available, otherwise read_file (text only).
func (e *ExternalMCP) ReadFileRaw(path string) ([]byte, string, error) {
	if e.hasTool("read_media_file") {
		return e.callReadTool("read_media_file", path)
	}
	return e.callReadTool("read_file", path)
}

// hasTool checks if the MCP server advertises a tool by name.
func (e *ExternalMCP) hasTool(name string) bool {
	for _, t := range e.tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// callReadTool calls an MCP read tool and extracts content from the response.
func (e *ExternalMCP) callReadTool(toolName, path string) ([]byte, string, error) {
	result, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      toolName,
		Arguments: map[string]interface{}{"path": path},
	})
	if err != nil {
		return nil, "", fmt.Errorf("%s %q on %q: %w", toolName, path, e.name, err)
	}
	if result.IsError {
		return nil, "", fmt.Errorf("%s %q error: %s", toolName, path, extractTextContent(result.Content))
	}

	for _, c := range result.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			// ic.Data is already raw bytes — Go's json decoder handles base64 for []byte fields
			mimeType := ic.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			return ic.Data, mimeType, nil
		}
		if ac, ok := c.(*mcp.AudioContent); ok {
			mimeType := ac.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			return ac.Data, mimeType, nil
		}
		if tc, ok := c.(*mcp.TextContent); ok {
			return []byte(tc.Text), "text/plain", nil
		}
	}

	return nil, "", fmt.Errorf("%s %q: no content in response", toolName, path)
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
