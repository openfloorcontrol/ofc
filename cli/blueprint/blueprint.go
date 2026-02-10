package blueprint

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Agent configuration
type Agent struct {
	ID          string  `yaml:"id"`
	Name        string  `yaml:"name"`
	Model       string  `yaml:"model"`
	Endpoint    string  `yaml:"endpoint"`
	Prompt      string  `yaml:"prompt"`
	Activation  string  `yaml:"activation"`
	CanUseTools bool    `yaml:"can_use_tools"`
	Temperature float64 `yaml:"temperature"`
	ToolContext string  `yaml:"tool_context"`
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

// Blueprint is a complete floor configuration
type Blueprint struct {
	Name         string        `yaml:"name"`
	Description  string        `yaml:"description"`
	Defaults     Defaults      `yaml:"defaults"`
	Agents       []Agent       `yaml:"agents"`
	Workstations []Workstation `yaml:"workstations"`
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
	}

	return &bp, nil
}
