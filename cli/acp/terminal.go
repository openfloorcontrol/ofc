package acp

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/openfloorcontrol/ofc/sandbox"
)

// Terminal tracks a single terminal session.
type Terminal struct {
	ID     string
	Cmd    string
	Output bytes.Buffer
	Done   chan struct{}
	Exit   int
	mu     sync.Mutex
}

// TerminalManager maps ACP's async terminal model to command execution.
// If a sandbox is provided, commands run inside the Docker container.
// Otherwise, commands run directly on the host.
type TerminalManager struct {
	sandbox   *sandbox.Sandbox // nil = run on host
	terminals map[string]*Terminal
	nextID    atomic.Uint64
	mu        sync.Mutex
}

// NewTerminalManager creates a terminal manager.
// If sandbox is nil, commands execute directly on the host.
func NewTerminalManager(s *sandbox.Sandbox) *TerminalManager {
	return &TerminalManager{
		sandbox:   s,
		terminals: make(map[string]*Terminal),
	}
}

// Create runs a command and returns a terminal ID.
func (tm *TerminalManager) Create(command string, args []string, cwd *string) (string, error) {
	id := fmt.Sprintf("term-%d", tm.nextID.Add(1))

	// Build the full command string
	fullCmd := command
	for _, a := range args {
		fullCmd += " " + a
	}

	term := &Terminal{
		ID:   id,
		Cmd:  fullCmd,
		Done: make(chan struct{}),
	}

	tm.mu.Lock()
	tm.terminals[id] = term
	tm.mu.Unlock()

	if tm.sandbox != nil {
		// Run in Docker sandbox
		sandboxCmd := fullCmd
		if cwd != nil && *cwd != "" {
			sandboxCmd = fmt.Sprintf("cd %s && %s", *cwd, fullCmd)
		}
		go func() {
			output, err := tm.sandbox.Execute(sandboxCmd)
			term.mu.Lock()
			term.Output.WriteString(output)
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					term.Exit = exitErr.ExitCode()
				} else {
					term.Exit = 1
				}
			}
			term.mu.Unlock()
			close(term.Done)
		}()
	} else {
		// Run directly on host
		go func() {
			cmd := exec.Command("bash", "-c", fullCmd)
			if cwd != nil && *cwd != "" {
				cmd.Dir = *cwd
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			term.mu.Lock()
			term.Output.WriteString(stdout.String())
			term.Output.WriteString(stderr.String())
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					term.Exit = exitErr.ExitCode()
				} else {
					term.Exit = 1
				}
			}
			term.mu.Unlock()
			close(term.Done)
		}()
	}

	return id, nil
}

// GetOutput returns the current buffered output for a terminal.
func (tm *TerminalManager) GetOutput(id string) (string, bool, error) {
	tm.mu.Lock()
	term, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		return "", false, fmt.Errorf("terminal %s not found", id)
	}

	term.mu.Lock()
	output := term.Output.String()
	term.mu.Unlock()

	return output, false, nil
}

// WaitForExit blocks until the terminal process finishes and returns the exit code.
func (tm *TerminalManager) WaitForExit(id string) (int, error) {
	tm.mu.Lock()
	term, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		return -1, fmt.Errorf("terminal %s not found", id)
	}

	<-term.Done
	term.mu.Lock()
	exit := term.Exit
	term.mu.Unlock()
	return exit, nil
}

// Kill attempts to kill a terminal's process.
func (tm *TerminalManager) Kill(id string) error {
	tm.mu.Lock()
	_, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		return fmt.Errorf("terminal %s not found", id)
	}
	return nil
}

// Release removes a terminal from tracking.
func (tm *TerminalManager) Release(id string) {
	tm.mu.Lock()
	delete(tm.terminals, id)
	tm.mu.Unlock()
}
