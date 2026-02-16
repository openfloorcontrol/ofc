package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultImage   = "python:3.11-slim"
	DefaultTimeout = 30 * time.Second
)

// Sandbox manages a Docker container for code execution
type Sandbox struct {
	ContainerID    string
	Image          string
	DockerfileDir  string // directory containing Dockerfile (empty = use Image directly)
	WorkspaceDir   string
	Timeout        time.Duration
}

// New creates a new sandbox
func New(workspaceDir, image, dockerfile string) *Sandbox {
	if image == "" {
		image = DefaultImage
	}
	return &Sandbox{
		Image:         image,
		DockerfileDir: dockerfile,
		WorkspaceDir:  workspaceDir,
		Timeout:       DefaultTimeout,
	}
}

// ensureImage builds the Docker image from Dockerfile if needed
func (s *Sandbox) ensureImage() error {
	if s.DockerfileDir == "" {
		return nil
	}

	// Resolve to directory containing the Dockerfile
	dockerfileDir := s.DockerfileDir
	info, err := os.Stat(dockerfileDir)
	if err != nil {
		return fmt.Errorf("dockerfile path not found: %s", dockerfileDir)
	}
	// If it points to a file, use its directory
	if !info.IsDir() {
		dockerfileDir = filepath.Dir(dockerfileDir)
	}

	dockerfilePath := filepath.Join(dockerfileDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); err != nil {
		return fmt.Errorf("Dockerfile not found: %s", dockerfilePath)
	}

	// Check if image exists and if Dockerfile is newer
	needsBuild := false
	imageTime := getImageCreatedTime(s.Image)
	if imageTime.IsZero() {
		needsBuild = true
	} else {
		dfInfo, err := os.Stat(dockerfilePath)
		if err == nil && dfInfo.ModTime().After(imageTime) {
			needsBuild = true
		}
	}

	if !needsBuild {
		return nil
	}

	fmt.Printf("\033[2m[System]: Building sandbox image (%s)...\033[0m\n", s.Image)
	cmd := exec.Command("docker", "build", "-t", s.Image, dockerfileDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	fmt.Printf("\033[2m[System]: Sandbox image ready\033[0m\n")
	return nil
}

// getImageCreatedTime returns the creation time of a Docker image, or zero time if not found
func getImageCreatedTime(image string) time.Time {
	cmd := exec.Command("docker", "inspect", "-f", "{{.Created}}", image)
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}
	}
	created := strings.TrimSpace(string(output))
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Start launches the sandbox container
func (s *Sandbox) Start() error {
	// Build image from Dockerfile if configured
	if err := s.ensureImage(); err != nil {
		return err
	}

	// Resolve workspace to absolute path for bind mount
	wsAbs := s.WorkspaceDir
	if wsAbs != "" {
		abs, err := filepath.Abs(wsAbs)
		if err == nil {
			wsAbs = abs
		}
		// Ensure the directory exists on host
		os.MkdirAll(wsAbs, 0o755)
	}

	// Start container with workspace bind-mounted at the same absolute path
	// so the agent can use real host paths and writes go through naturally.
	args := []string{"run", "-d", "--rm", "-w", wsAbs}
	if wsAbs != "" {
		args = append(args, "-v", wsAbs+":"+wsAbs)
	}
	args = append(args, s.Image, "sleep", "infinity")
	cmd := exec.Command("docker", args...)

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to start container (image: %s): %w", s.Image, err)
	}

	s.ContainerID = strings.TrimSpace(string(output))

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
