package acp

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/openfloorcontrol/ofc/sandbox"
)

// Terminal tracks a single terminal session backed by a docker exec process.
type Terminal struct {
	ID     string
	Cmd    string
	Args   []string
	Output bytes.Buffer
	Done   chan struct{}
	Exit   int
	mu     sync.Mutex
}

// TerminalManager maps ACP's async terminal model to sandbox docker exec processes.
type TerminalManager struct {
	sandbox   *sandbox.Sandbox
	terminals map[string]*Terminal
	nextID    atomic.Uint64
	mu        sync.Mutex
}

// NewTerminalManager creates a terminal manager backed by the given sandbox.
func NewTerminalManager(s *sandbox.Sandbox) *TerminalManager {
	return &TerminalManager{
		sandbox:   s,
		terminals: make(map[string]*Terminal),
	}
}

// Create runs a command in the sandbox and returns a terminal ID.
func (tm *TerminalManager) Create(command string, args []string, cwd *string) (string, error) {
	id := fmt.Sprintf("term-%d", tm.nextID.Add(1))

	// Build the full command string for docker exec
	fullCmd := command
	for _, a := range args {
		fullCmd += " " + a
	}
	if cwd != nil && *cwd != "" {
		fullCmd = fmt.Sprintf("cd %s && %s", *cwd, fullCmd)
	}

	term := &Terminal{
		ID:   id,
		Cmd:  fullCmd,
		Done: make(chan struct{}),
	}

	tm.mu.Lock()
	tm.terminals[id] = term
	tm.mu.Unlock()

	// Run asynchronously in the sandbox
	go func() {
		output, err := tm.sandbox.Execute(fullCmd)
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

// Kill attempts to kill a terminal's process (best-effort via sandbox timeout).
func (tm *TerminalManager) Kill(id string) error {
	tm.mu.Lock()
	_, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		return fmt.Errorf("terminal %s not found", id)
	}
	// Sandbox.Execute is blocking and has its own timeout; nothing to kill here
	// In future, we could track the actual docker exec PID
	return nil
}

// Release removes a terminal from tracking.
func (tm *TerminalManager) Release(id string) {
	tm.mu.Lock()
	delete(tm.terminals, id)
	tm.mu.Unlock()
}
