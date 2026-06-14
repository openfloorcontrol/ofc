// Package blueprint defines the blueprint schema and loading for OFC floors.
package blueprint

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// expandPromptTemplate expands Go template syntax in a prompt using <% %> delimiters.
// Returns the input unchanged if no <% marker is found (zero overhead for plain prompts).
//
// Available functions:
//   - readfile "path" — read a file (paths relative to blueprint directory)
//   - env "VAR"      — read an environment variable
//
// Example:
//
//	You are a camera expert.
//
//	Available products:
//	<% readfile "data/catalog.md" %>
func expandPromptTemplate(prompt string, bpDir string) (string, error) {
	if !strings.Contains(prompt, "<%") {
		return prompt, nil
	}

	funcs := template.FuncMap{
		"readfile": func(path string) (string, error) {
			if !filepath.IsAbs(path) {
				path = filepath.Join(bpDir, path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
		"env": os.Getenv,
	}

	tmpl, err := template.New("prompt").Delims("<%", "%>").Funcs(funcs).Parse(prompt)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// Agent configuration
type Agent struct {
	ID          string            `yaml:"id"`
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`    // "llm" (default) or "acp"
	Model       string            `yaml:"model"`
	Endpoint    string            `yaml:"endpoint"`
	APIKey      string            `yaml:"api_key,omitempty"` // supports ${VAR} env expansion
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
	APIKey   string `yaml:"api_key,omitempty"` // supports ${VAR} env expansion
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

// Config holds runtime/deployment knobs — things that historically came
// from CLI flags or env vars. Lives inside the blueprint so each project
// can carry its own "how to run this" defaults. Per-invocation knobs
// (--file, --session, the initial prompt) are intentionally NOT here.
//
// Future: profiles (dev/prod overlays) and ~/.ofc/defaults.yaml will
// layer above and below this, respectively. Not implemented yet — when
// they land, precedence will be:
//   CLI flag (if explicitly set) > env > profile > config > defaults.yaml > built-in
//
// Today there is one layer of precedence: a CLI flag wins when
// explicitly set (cobra Changed()); otherwise Config provides the value.
type Config struct {
	// Frontend selects the terminal frontend: "cli" (default), "tui",
	// or "json".
	Frontend string `yaml:"frontend,omitempty"`

	Web   WebConfig   `yaml:"web,omitempty"`
	Store StoreConfig `yaml:"store,omitempty"`

	// Debug toggles per-component debug output.
	Debug bool `yaml:"debug,omitempty"`

	// Log is an output log path. Empty means no log file.
	Log string `yaml:"log,omitempty"`
}

// WebConfig groups web-UI settings.
type WebConfig struct {
	Enabled  bool   `yaml:"enabled,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	Hostname string `yaml:"hostname,omitempty"` // external URL for printed link
}

// StoreConfig selects the session-store backend.
type StoreConfig struct {
	// Type is "jsonl" (default) or "postgres".
	Type string `yaml:"type,omitempty"`
	// DSN is the Postgres connection string when Type is "postgres".
	// Supports ${VAR} env expansion so secrets stay out of the blueprint.
	DSN string `yaml:"dsn,omitempty"`
}

// Blueprint is a complete floor configuration
type Blueprint struct {
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	Defaults     Defaults       `yaml:"defaults"`
	Config       Config         `yaml:"config,omitempty"`
	Agents       []Agent        `yaml:"agents"`
	Workstations []Workstation  `yaml:"workstations"`
	Furniture    []FurnitureDef `yaml:"furniture,omitempty"`

	// Dir is the absolute directory containing the blueprint file.
	// Used as the cwd for subprocesses (MCP servers, ACP agents) so that
	// relative paths in command/args resolve correctly regardless of where
	// `ofc` is invoked from. Set automatically by Load.
	Dir string `yaml:"-"`
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

	// Resolve prompt files relative to blueprint directory, then expand templates
	bpDir := filepath.Dir(path)
	if absDir, err := filepath.Abs(bpDir); err == nil {
		bp.Dir = absDir
	} else {
		bp.Dir = bpDir
	}
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
		if bp.Agents[i].Prompt != "" {
			expanded, err := expandPromptTemplate(bp.Agents[i].Prompt, bpDir)
			if err != nil {
				return nil, fmt.Errorf("agent %s: prompt template: %w", bp.Agents[i].ID, err)
			}
			bp.Agents[i].Prompt = expanded
		}
	}

	// Resolve workstation paths relative to blueprint directory
	for i := range bp.Workstations {
		ws := &bp.Workstations[i]
		if ws.Dockerfile != "" && !filepath.IsAbs(ws.Dockerfile) {
			ws.Dockerfile = filepath.Join(bpDir, ws.Dockerfile)
		}
		if ws.Mount != "" {
			// Mount format: "host:container" — resolve host part
			parts := strings.SplitN(ws.Mount, ":", 2)
			if len(parts) == 2 && !filepath.IsAbs(parts[0]) {
				parts[0] = filepath.Join(bpDir, parts[0])
				ws.Mount = parts[0] + ":" + parts[1]
			}
		}
	}

	// Expand environment variables in API keys and store DSN.
	bp.Defaults.APIKey = os.ExpandEnv(bp.Defaults.APIKey)
	bp.Config.Store.DSN = os.ExpandEnv(bp.Config.Store.DSN)

	// Apply defaults
	for i := range bp.Agents {
		if bp.Agents[i].Endpoint == "" {
			bp.Agents[i].Endpoint = bp.Defaults.Endpoint
		}
		if bp.Agents[i].Model == "" {
			bp.Agents[i].Model = bp.Defaults.Model
		}
		if bp.Agents[i].APIKey == "" {
			bp.Agents[i].APIKey = bp.Defaults.APIKey
		} else {
			bp.Agents[i].APIKey = os.ExpandEnv(bp.Agents[i].APIKey)
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
