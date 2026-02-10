package sandbox

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultImage   = "mac-sandbox:latest"
	DefaultTimeout = 30 * time.Second
)

// Sandbox manages a Docker container for code execution
type Sandbox struct {
	ContainerID  string
	Image        string
	WorkspaceDir string
	Timeout      time.Duration
}

// New creates a new sandbox
func New(workspaceDir string) *Sandbox {
	return &Sandbox{
		Image:        DefaultImage,
		WorkspaceDir: workspaceDir,
		Timeout:      DefaultTimeout,
	}
}

// Start launches the sandbox container
func (s *Sandbox) Start() error {
	// Start container
	cmd := exec.Command("docker", "run", "-d", "--rm",
		"-w", "/workspace",
		s.Image,
		"sleep", "infinity",
	)

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	s.ContainerID = strings.TrimSpace(string(output))

	// Copy workspace if provided
	if s.WorkspaceDir != "" {
		copyCmd := exec.Command("docker", "cp",
			s.WorkspaceDir+"/.",
			s.ContainerID+":/workspace/",
		)
		if err := copyCmd.Run(); err != nil {
			// Non-fatal, workspace might not exist
		}
	}

	return nil
}

// Execute runs a command in the sandbox
func (s *Sandbox) Execute(command string) (string, error) {
	if s.ContainerID == "" {
		return "", fmt.Errorf("sandbox not started")
	}

	cmd := exec.Command("docker", "exec", s.ContainerID, "bash", "-c", command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Create a channel for the result
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	// Wait with timeout
	select {
	case err := <-done:
		output := stdout.String() + stderr.String()
		if output == "" {
			output = "[no output]"
		}
		// Truncate if too long
		if len(output) > 10000 {
			output = output[:5000] + "\n... [truncated] ...\n" + output[len(output)-2000:]
		}
		if err != nil {
			return strings.TrimSpace(output), nil // Return output even on error
		}
		return strings.TrimSpace(output), nil

	case <-time.After(s.Timeout):
		cmd.Process.Kill()
		return "", fmt.Errorf("command timed out after %v", s.Timeout)
	}
}

// Stop kills the sandbox container
func (s *Sandbox) Stop() error {
	if s.ContainerID == "" {
		return nil
	}

	cmd := exec.Command("docker", "kill", s.ContainerID)
	cmd.Run() // Ignore errors
	s.ContainerID = ""
	return nil
}

// CopyOut copies files from the container to the host
func (s *Sandbox) CopyOut(containerPath, hostPath string) error {
	cmd := exec.Command("docker", "cp",
		s.ContainerID+":"+containerPath,
		hostPath,
	)
	return cmd.Run()
}
