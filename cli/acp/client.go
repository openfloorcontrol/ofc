package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// ToolInteraction records one tool call and its result for floor-level tracking.
type ToolInteraction struct {
	Command string
	Output  string
}

// FloorClient implements the acp.Client interface.
// It handles callbacks from ACP agents by proxying to sandbox/filesystem.
type FloorClient struct {
	Sandbox      *sandbox.Sandbox
	WorkspaceDir string
	Terminals    *TerminalManager
	DebugFunc    func(string) // if set, debug messages are routed here
	LogWriter    io.Writer    // optional log file writer (plain text, no ANSI)

	// Per-prompt state (set before each Prompt call, reset after)
	OnToken      func(string)
	OnToolCall   func(title string)
	OnToolResult func(title, output string)
	ResponseText strings.Builder
	Interactions []ToolInteraction
	toolCalls    map[string]string // toolCallId → title, for tracking in-flight calls

	mu sync.Mutex
}

var _ acpsdk.Client = (*FloorClient)(nil)

// NewFloorClient creates a new floor client that handles ACP callbacks.
// TerminalManager is always created — it supports both sandbox and host execution.
func NewFloorClient(sb *sandbox.Sandbox, workspaceDir string) *FloorClient {
	return &FloorClient{
		Sandbox:      sb,
		WorkspaceDir: workspaceDir,
		Terminals:    NewTerminalManager(sb),
		toolCalls:    make(map[string]string),
	}
}

// Reset clears per-prompt state before a new prompt.
func (c *FloorClient) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ResponseText.Reset()
	c.Interactions = nil
	c.toolCalls = make(map[string]string)
}

func (c *FloorClient) debug(msg string) {
	if c.DebugFunc != nil {
		c.DebugFunc(msg)
	}
	if c.LogWriter != nil {
		fmt.Fprintf(c.LogWriter, "  [acp] %s\n", msg)
	}
}


// --- acp.Client interface ---

func (c *FloorClient) SessionUpdate(ctx context.Context, params acpsdk.SessionNotification) error {
	u := params.Update

	switch {
	case u.AgentMessageChunk != nil:
		if u.AgentMessageChunk.Content.Text != nil {
			text := u.AgentMessageChunk.Content.Text.Text
			c.mu.Lock()
			c.ResponseText.WriteString(text)
			onToken := c.OnToken
			c.mu.Unlock()
			if onToken != nil {
				onToken(text)
			}
		}

	case u.ToolCall != nil:
		c.debug(fmt.Sprintf("tool_call: %s (%s)", u.ToolCall.Title, u.ToolCall.Status))
		// Track the tool call start so we can pair it with output later
		c.mu.Lock()
		c.toolCalls[string(u.ToolCall.ToolCallId)] = u.ToolCall.Title
		c.mu.Unlock()
		// Print tool call title to output
		if c.OnToolCall != nil {
			c.OnToolCall(u.ToolCall.Title)
		}

	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		c.debug(fmt.Sprintf("tool_call_update: %s status=%s", u.ToolCallUpdate.ToolCallId, status))
		// When a tool call completes, record it as an interaction and print result
		if u.ToolCallUpdate.Status != nil && *u.ToolCallUpdate.Status == acpsdk.ToolCallStatusCompleted {
			c.mu.Lock()
			tcID := string(u.ToolCallUpdate.ToolCallId)
			title := c.toolCalls[tcID]
			output := extractToolCallText(u.ToolCallUpdate.Content)
			c.Interactions = append(c.Interactions, ToolInteraction{
				Command: title,
				Output:  output,
			})
			delete(c.toolCalls, tcID)
			c.mu.Unlock()
			if c.OnToolResult != nil {
				c.OnToolResult(title, output)
			}
		}

	case u.AgentThoughtChunk != nil:
		// Silently consume thoughts
	case u.Plan != nil:
		// Silently consume plan updates
	}

	return nil
}

func (c *FloorClient) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	// Auto-approve for v1
	c.debug("auto-approving permission request")
	if len(params.Options) > 0 {
		// Find the first "allow" option
		for _, opt := range params.Options {
			if opt.Kind == acpsdk.PermissionOptionKindAllowOnce || opt.Kind == acpsdk.PermissionOptionKindAllowAlways {
				return acpsdk.RequestPermissionResponse{
					Outcome: acpsdk.RequestPermissionOutcome{
						Selected: &acpsdk.RequestPermissionOutcomeSelected{
							OptionId: opt.OptionId,
							Outcome:  "selected",
						},
					},
				}, nil
			}
		}
		// Fallback: pick first option
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.RequestPermissionOutcome{
				Selected: &acpsdk.RequestPermissionOutcomeSelected{
					OptionId: params.Options[0].OptionId,
					Outcome:  "selected",
				},
			},
		}, nil
	}
	return acpsdk.RequestPermissionResponse{}, nil
}

// --- File system callbacks ---

func (c *FloorClient) ReadTextFile(ctx context.Context, params acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.WorkspaceDir, path)
	}
	c.debug(fmt.Sprintf("fs/read: %s", path))

	var content string
	if c.Sandbox != nil {
		// Read from sandbox container
		output, err := c.Sandbox.Execute(fmt.Sprintf("cat %q", path))
		if err != nil {
			return acpsdk.ReadTextFileResponse{}, fmt.Errorf("read %s in sandbox: %w", path, err)
		}
		content = output
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return acpsdk.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", path, err)
		}
		content = string(data)
	}

	// Apply line/limit if specified
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = *params.Line - 1
			if start > len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 {
			if start+*params.Limit < end {
				end = start + *params.Limit
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return acpsdk.ReadTextFileResponse{Content: content}, nil
}

func (c *FloorClient) WriteTextFile(ctx context.Context, params acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.WorkspaceDir, path)
	}
	c.debug(fmt.Sprintf("fs/write: %s (%d bytes)", path, len(params.Content)))

	if c.Sandbox != nil {
		// Write into sandbox container
		dir := filepath.Dir(path)
		_, _ = c.Sandbox.Execute(fmt.Sprintf("mkdir -p %q", dir))
		// Use heredoc to avoid shell escaping issues with content
		cmd := fmt.Sprintf("cat > %q << 'OFC_EOF'\n%s\nOFC_EOF", path, params.Content)
		_, err := c.Sandbox.Execute(cmd)
		if err != nil {
			return acpsdk.WriteTextFileResponse{}, fmt.Errorf("write %s in sandbox: %w", path, err)
		}
	} else {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return acpsdk.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
			return acpsdk.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", path, err)
		}
	}

	return acpsdk.WriteTextFileResponse{}, nil
}

// --- Terminal callbacks ---

func (c *FloorClient) CreateTerminal(ctx context.Context, params acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	c.debug(fmt.Sprintf("terminal/create: %s %v", params.Command, params.Args))

	id, err := c.Terminals.Create(params.Command, params.Args, params.Cwd)
	if err != nil {
		return acpsdk.CreateTerminalResponse{}, err
	}

	return acpsdk.CreateTerminalResponse{TerminalId: id}, nil
}

func (c *FloorClient) TerminalOutput(ctx context.Context, params acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	output, truncated, err := c.Terminals.GetOutput(params.TerminalId)
	if err != nil {
		return acpsdk.TerminalOutputResponse{}, err
	}

	return acpsdk.TerminalOutputResponse{
		Output:    output,
		Truncated: truncated,
	}, nil
}

func (c *FloorClient) WaitForTerminalExit(ctx context.Context, params acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	exitCode, err := c.Terminals.WaitForExit(params.TerminalId)
	if err != nil {
		return acpsdk.WaitForTerminalExitResponse{}, err
	}

	return acpsdk.WaitForTerminalExitResponse{
		ExitCode: &exitCode,
	}, nil
}

func (c *FloorClient) KillTerminalCommand(ctx context.Context, params acpsdk.KillTerminalCommandRequest) (acpsdk.KillTerminalCommandResponse, error) {
	_ = c.Terminals.Kill(params.TerminalId)
	return acpsdk.KillTerminalCommandResponse{}, nil
}

func (c *FloorClient) ReleaseTerminal(ctx context.Context, params acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	c.Terminals.Release(params.TerminalId)
	return acpsdk.ReleaseTerminalResponse{}, nil
}

// extractToolCallText extracts text content from a ToolCallContent slice.
func extractToolCallText(content []acpsdk.ToolCallContent) string {
	var sb strings.Builder
	for _, c := range content {
		if c.Content != nil && c.Content.Content.Text != nil {
			sb.WriteString(c.Content.Content.Text.Text)
		}
	}
	return sb.String()
}
