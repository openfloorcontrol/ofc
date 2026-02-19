package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	acpsdk "github.com/coder/acp-go-sdk"
)

// AgentSession manages the lifecycle of one ACP agent connection.
type AgentSession struct {
	Conn      *acpsdk.ClientSideConnection
	SessionID acpsdk.SessionId
	Cmd       *exec.Cmd
	Client    *FloorClient
}

// NewAgentSession launches an ACP agent process and establishes a connection.
// stderrWriter receives the agent's stderr output. If nil, defaults to os.Stderr.
func NewAgentSession(command string, args []string, env map[string]string, client *FloorClient, stderrWriter io.Writer) (*AgentSession, error) {
	cmd := exec.Command(command, args...)
	if stderrWriter != nil {
		cmd.Stderr = stderrWriter
	} else {
		cmd.Stderr = os.Stderr
	}

	// Pass through environment, with overrides
	cmd.Env = os.Environ()
	for k, v := range env {
		// Expand environment variable references like ${ANTHROPIC_API_KEY}
		expanded := os.ExpandEnv(v)
		cmd.Env = append(cmd.Env, k+"="+expanded)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent %q: %w", command, err)
	}

	conn := acpsdk.NewClientSideConnection(client, stdin, stdout)

	return &AgentSession{
		Conn:   conn,
		Cmd:    cmd,
		Client: client,
	}, nil
}

// Initialize performs the ACP handshake, advertising filesystem and terminal capabilities.
func (s *AgentSession) Initialize(ctx context.Context) error {
	resp, err := s.Conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: true,
		},
		ClientInfo: &acpsdk.Implementation{
			Name:    "ofc",
			Version: "0.1.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	s.Client.debug(fmt.Sprintf("initialized: protocol v%d, agent=%v", resp.ProtocolVersion, resp.AgentCapabilities))
	return nil
}

// StartSession creates a new ACP session with the given working directory.
func (s *AgentSession) StartSession(ctx context.Context, cwd string) error {
	resp, err := s.Conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}

	s.SessionID = resp.SessionId
	s.Client.debug(fmt.Sprintf("session created: %s", s.SessionID))
	return nil
}

// Prompt sends content blocks to the agent and blocks until it finishes.
// Streaming happens via the Client's SessionUpdate callback.
// Returns the stop reason.
func (s *AgentSession) Prompt(ctx context.Context, blocks []acpsdk.ContentBlock) (acpsdk.StopReason, error) {
	resp, err := s.Conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: s.SessionID,
		Prompt:    blocks,
	})
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	return resp.StopReason, nil
}

// Close kills the agent process and waits for cleanup.
func (s *AgentSession) Close() error {
	if s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
		_ = s.Cmd.Wait()
	}
	return nil
}
