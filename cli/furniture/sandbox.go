package furniture

import (
	"fmt"

	"github.com/openfloorcontrol/ofc/sandbox"
)

// Sandbox is furniture around a Docker sandbox. Its one tool, bash, runs
// a command in the container. Close stops the container.
type Sandbox struct {
	name string
	sb   *sandbox.Sandbox
}

// NewSandbox wraps a started sandbox as furniture called name.
func NewSandbox(name string, sb *sandbox.Sandbox) *Sandbox {
	return &Sandbox{name: name, sb: sb}
}

func (s *Sandbox) Name() string { return s.name }

// Container returns the sandbox the furniture runs commands in.
func (s *Sandbox) Container() *sandbox.Sandbox { return s.sb }

func (s *Sandbox) Tools() []Tool {
	return []Tool{{
		Name:        "bash",
		Description: fmt.Sprintf("Run a bash command in the sandbox container; the working directory is %s.", s.sb.WorkspaceDir),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cmd": map[string]interface{}{
					"type":        "string",
					"description": "The bash command to execute",
				},
			},
			"required": []string{"cmd"},
		},
	}}
}

func (s *Sandbox) Call(toolName string, args map[string]interface{}) (interface{}, error) {
	if toolName != "bash" {
		return nil, &ErrUnknownTool{Furniture: s.name, Tool: toolName}
	}
	cmd, ok := args["cmd"].(string)
	if !ok {
		return nil, fmt.Errorf("bash: cmd must be a string")
	}
	return s.sb.Execute(cmd)
}

func (s *Sandbox) Close() error { return s.sb.Stop() }
