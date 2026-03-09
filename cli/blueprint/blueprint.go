// Package blueprint defines the blueprint schema and loading for OFC floors.
package blueprint

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Agent configuration
type Agent struct {
	ID          string            `yaml:"id"`
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`    // "llm" (default) or "acp"
	Model       string            `yaml:"model"`
	Endpoint    string            `yaml:"endpoint"`
	Command     string            `yaml:"command"` // ACP: command to launch agent
	Args        []string          `yaml:"args"`    // ACP: args for the command
	Env         map[string]string `yaml:"env"`     // ACP: env vars for agent process
	Prompt      string            `yaml:"prompt"`
	PromptFile  string            `yaml:"prompt_file"`
	Activation  string            `yaml:"activation"`
	CanUseSandbox bool            `yaml:"can_use_sandbox"`
	Temperature float64           `yaml:"temperature"`
	ToolContext string            `yaml:"tool_context"`
	Furniture   []string          `yaml:"furniture,omitempty"` // names of accessible furniture
}

// Workstation configuration
type Workstation struct {
	Type       string `yaml:"type"`
	Name       string `yaml:"name"`
	Image      string `yaml:"image"`
	Dockerfile string `yaml:"dockerfile"`
	Mount      string `yaml:"mount"`
}

// Defaults for the blueprint
type Defaults struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
}

// FurnitureDef configures a piece of furniture on the floor.
type FurnitureDef struct {
	Name    string            `yaml:"name"`              // identifier (e.g. "tasks")
	Type    string            `yaml:"type"`              // "taskboard", "mcp", etc.
	Command string            `yaml:"command,omitempty"` // executable for external MCP servers (stdio)
	Args    []string          `yaml:"args,omitempty"`    // arguments for external MCP command
	URL     string            `yaml:"url,omitempty"`     // URL for already-running MCP servers (HTTP)
	Headers map[string]string `yaml:"headers,omitempty"` // HTTP headers (supports ${VAR} env expansion)
	Config  map[string]string `yaml:"config,omitempty"`  // type-specific configuration
}

// Blueprint is a complete floor configuration
type Blueprint struct {
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	Defaults     Defaults       `yaml:"defaults"`
	Agents       []Agent        `yaml:"agents"`
	Workstations []Workstation  `yaml:"workstations"`
	Furniture    []FurnitureDef `yaml:"furniture,omitempty"`
}

// Load reads a blueprint from a YAML file
func Load(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return nil, err
	}

	// Resolve prompt files relative to blueprint directory
	bpDir := filepath.Dir(path)
	for i := range bp.Agents {
		if bp.Agents[i].PromptFile != "" {
			if bp.Agents[i].Prompt != "" {
				return nil, fmt.Errorf("agent %s: cannot set both prompt and prompt_file", bp.Agents[i].ID)
			}
			p := bp.Agents[i].PromptFile
			if !filepath.IsAbs(p) {
				p = filepath.Join(bpDir, p)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("agent %s: reading prompt_file: %w", bp.Agents[i].ID, err)
			}
			bp.Agents[i].Prompt = string(data)
		}
	}

	// Apply defaults
	for i := range bp.Agents {
		if bp.Agents[i].Endpoint == "" {
			bp.Agents[i].Endpoint = bp.Defaults.Endpoint
		}
		if bp.Agents[i].Model == "" {
			bp.Agents[i].Model = bp.Defaults.Model
		}
		if bp.Agents[i].Temperature == 0 {
			bp.Agents[i].Temperature = 0.7
		}
		if bp.Agents[i].Activation == "" {
			bp.Agents[i].Activation = "mention"
		}
		if bp.Agents[i].ToolContext == "" {
			bp.Agents[i].ToolContext = "full"
		}
		if bp.Agents[i].Type == "" {
			bp.Agents[i].Type = "llm"
		}
	}

	return &bp, nil
}
