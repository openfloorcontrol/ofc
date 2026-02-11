package acp

import (
	"context"
	"fmt"
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
	Debug        bool

	// Per-prompt state (set before each Prompt call, reset after)
	OnToken      func(string)
	ResponseText strings.Builder
	Interactions []ToolInteraction

	mu sync.Mutex
}

var _ acpsdk.Client = (*FloorClient)(nil)

// NewFloorClient creates a new floor client that handles ACP callbacks.
func NewFloorClient(sb *sandbox.Sandbox, workspaceDir string, debug bool) *FloorClient {
	fc := &FloorClient{
		Sandbox:      sb,
		WorkspaceDir: workspaceDir,
		Debug:        debug,
	}
	if sb != nil {
		fc.Terminals = NewTerminalManager(sb)
	}
	return fc
}

// Reset clears per-prompt state before a new prompt.
func (c *FloorClient) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ResponseText.Reset()
	c.Interactions = nil
}

func (c *FloorClient) debug(msg string) {
	if c.Debug {
		fmt.Printf("  [acp-debug] %s\n", msg)
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

	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		c.debug(fmt.Sprintf("tool_call_update: %s status=%s", u.ToolCallUpdate.ToolCallId, status))

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

	data, err := os.ReadFile(path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", path, err)
	}

	content := string(data)

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

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", path, err)
	}

	return acpsdk.WriteTextFileResponse{}, nil
}

// --- Terminal callbacks ---

func (c *FloorClient) CreateTerminal(ctx context.Context, params acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	if c.Terminals == nil {
		return acpsdk.CreateTerminalResponse{}, fmt.Errorf("no sandbox available for terminal")
	}
	c.debug(fmt.Sprintf("terminal/create: %s %v", params.Command, params.Args))

	id, err := c.Terminals.Create(params.Command, params.Args, params.Cwd)
	if err != nil {
		return acpsdk.CreateTerminalResponse{}, err
	}

	return acpsdk.CreateTerminalResponse{TerminalId: id}, nil
}

func (c *FloorClient) TerminalOutput(ctx context.Context, params acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	if c.Terminals == nil {
		return acpsdk.TerminalOutputResponse{}, fmt.Errorf("no sandbox available")
	}

	output, truncated, err := c.Terminals.GetOutput(params.TerminalId)
	if err != nil {
		return acpsdk.TerminalOutputResponse{}, err
	}

	// Record as tool interaction for floor message tracking
	c.mu.Lock()
	// Only record if we have new output
	if output != "" {
		c.Interactions = append(c.Interactions, ToolInteraction{
			Command: params.TerminalId,
			Output:  output,
		})
	}
	c.mu.Unlock()

	return acpsdk.TerminalOutputResponse{
		Output:    output,
		Truncated: truncated,
	}, nil
}

func (c *FloorClient) WaitForTerminalExit(ctx context.Context, params acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	if c.Terminals == nil {
		return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("no sandbox available")
	}

	exitCode, err := c.Terminals.WaitForExit(params.TerminalId)
	if err != nil {
		return acpsdk.WaitForTerminalExitResponse{}, err
	}

	return acpsdk.WaitForTerminalExitResponse{
		ExitCode: &exitCode,
	}, nil
}

func (c *FloorClient) KillTerminalCommand(ctx context.Context, params acpsdk.KillTerminalCommandRequest) (acpsdk.KillTerminalCommandResponse, error) {
	if c.Terminals == nil {
		return acpsdk.KillTerminalCommandResponse{}, fmt.Errorf("no sandbox available")
	}
	_ = c.Terminals.Kill(params.TerminalId)
	return acpsdk.KillTerminalCommandResponse{}, nil
}

func (c *FloorClient) ReleaseTerminal(ctx context.Context, params acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	if c.Terminals == nil {
		return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("no sandbox available")
	}
	c.Terminals.Release(params.TerminalId)
	return acpsdk.ReleaseTerminalResponse{}, nil
}
