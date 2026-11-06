// Package blueprint defines the blueprint schema and loading for OFC floors.
package blueprint

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// validName matches blueprint names, which are used unescaped in URLs.
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// parsePromptTemplate parses a prompt as a Go template using <% %> delimiters.
// Paths given to readfile resolve against dir.
//
// Available functions:
//   - readfile "path"     — read a file (paths relative to blueprint directory)
//   - env "VAR"           — read an environment variable; unset is an error
//   - env "VAR" "default" — read an environment variable, with a fallback
//
// The env function requires a reference to resolve unless a default is given,
// matching the ${VAR} / ${VAR-default} policy in env.go.
//
// Example:
//
//	You are a camera expert.
//
//	Available products:
//	<% readfile "data/catalog.md" %>
func parsePromptTemplate(prompt string, dir string) (*template.Template, error) {
	funcs := template.FuncMap{
		"readfile": func(path string) (string, error) {
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
		"env": func(name string, def ...string) (string, error) {
			if val, ok := os.LookupEnv(name); ok {
				return val, nil
			}
			if len(def) > 0 {
				return def[0], nil
			}
			return "", fmt.Errorf("environment variable %s is not set "+
				"(use <%% env %q \"default\" %%> to make it optional)", name, name)
		},
	}

	tmpl, err := template.New("prompt").Delims("<%", "%>").Funcs(funcs).Parse(prompt)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	return tmpl, nil
}

// RenderPrompt produces the agent's system prompt for the current turn.
// Templates are rendered per turn, so readfile and env see the state of the
// world at that moment. A prompt with no <% marker is returned as-is.
//
// Load parses and caches the template, so a syntax error surfaces at startup;
// an agent built without Load parses on demand.
func (a *Agent) RenderPrompt() (string, error) {
	if !strings.Contains(a.Prompt, "<%") {
		return a.Prompt, nil
	}

	tmpl := a.tmpl
	if tmpl == nil {
		var err error
		if tmpl, err = parsePromptTemplate(a.Prompt, a.dir); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// Agent configuration
type Agent struct {
	// Ref points to a standalone agent YAML file. When set, the blueprint
	// entry is replaced by the contents of that file at load time; other
	// fields on the entry are ignored. Path resolves relative to the
	// blueprint directory. Referenced files are read as-is — ${VAR}
	// expansion runs on the blueprint only, so env-coupled fields
	// (api_key, endpoint) belong in defaults.
	Ref string `yaml:"ref,omitempty"`

	// Enabled: false drops the agent when the blueprint loads, as if the
	// entry were not there. Omitted means enabled.
	Enabled *bool `yaml:"enabled,omitempty"`

	ID       string            `yaml:"id"`
	Name     string            `yaml:"name"`
	Type     string            `yaml:"type"` // "llm" (default) or "acp"
	Model    string            `yaml:"model"`
	Endpoint string            `yaml:"endpoint"`
	APIKey   string            `yaml:"api_key,omitempty"`
	Command  string            `yaml:"command"` // ACP: command to launch agent
	Args     []string          `yaml:"args"`    // ACP: args for the command
	Env      map[string]string `yaml:"env"`     // ACP: env vars for agent process
	// Prompt is the agent's system prompt. It may be a <% %> template, which
	// is parsed at load and rendered on every turn — see RenderPrompt.
	Prompt     string `yaml:"prompt"`
	PromptFile string `yaml:"prompt_file"`
	Activation string `yaml:"activation"`
	// Sandbox names a furniture of type sandbox in which an ACP agent's
	// file and terminal requests run. Empty runs them on the host.
	Sandbox string `yaml:"sandbox,omitempty"`
	// Thinking selects how reasoning is separated from the answer:
	// "auto" (default), "tags", "field" or "none".
	Thinking string `yaml:"thinking,omitempty"`
	// ThinkingTags overrides the inline tag pair, e.g. ["<think>", "</think>"].
	ThinkingTags []string `yaml:"thinking_tags,omitempty"`
	Temperature  float64  `yaml:"temperature"`
	ToolContext  string   `yaml:"tool_context"`
	Furniture    []string `yaml:"furniture,omitempty"` // names of the furniture the agent may use

	// dir is the blueprint directory, used to resolve readfile paths at
	// render time. tmpl is the parsed prompt template, nil when the prompt
	// has no <% marker. Both are set by Load.
	dir  string
	tmpl *template.Template
}

// Defaults for the blueprint
type Defaults struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key,omitempty"` // supports ${VAR} env expansion
	Thinking string `yaml:"thinking,omitempty"`
}

// FurnitureDef configures a piece of furniture on the floor.
type FurnitureDef struct {
	// Enabled: false drops the furniture when the blueprint loads, as if
	// the entry were not there. Omitted means enabled.
	Enabled *bool `yaml:"enabled,omitempty"`

	Name    string            `yaml:"name"`              // identifier (e.g. "tasks")
	Type    string            `yaml:"type"`              // "taskboard", "mcp", etc.
	Command string            `yaml:"command,omitempty"` // executable for external MCP servers (stdio)
	Args    []string          `yaml:"args,omitempty"`    // arguments for external MCP command
	URL     string            `yaml:"url,omitempty"`     // URL for already-running MCP servers (HTTP)
	Headers map[string]string `yaml:"headers,omitempty"` // HTTP headers (supports ${VAR} env expansion)
	Config  map[string]string `yaml:"config,omitempty"`  // type-specific configuration
	OAuth   *OAuthConfig      `yaml:"oauth,omitempty"`   // OAuth for a URL-based MCP server

	// Sandbox furniture: a Docker container from Image. With Dockerfile
	// (a Dockerfile or its directory), ofc builds Image from it when the
	// image is missing or older than the Dockerfile. Workspace is a host
	// directory mounted into the container at the same absolute path
	// (default ./workspace, relative to where ofc runs).
	Image      string `yaml:"image,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
	Workspace  string `yaml:"workspace,omitempty"`
}

// OAuthConfig makes a URL-based MCP furniture authorize with OAuth.
type OAuthConfig struct {
	// Grant is "authorization_code" (default): a person consents once,
	// with `ofc auth` or when an interactive `ofc run` asks; ofc then keeps
	// and refreshes the token. "client_credentials" needs no person and
	// requires ClientID and ClientSecret.
	Grant string `yaml:"grant,omitempty"`
	// ClientID and ClientSecret name a client registered with the
	// authorization server. Without ClientID, ofc registers itself
	// (dynamic client registration).
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	// Scopes to request; empty requests what the server advertises.
	Scopes []string `yaml:"scopes,omitempty"`
}

// Blueprint is a complete floor configuration: what the floor is. How a
// floor is run (frontend, ports, session store) is set by ofc's flags.
type Blueprint struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Defaults    Defaults       `yaml:"defaults"`
	Agents      []Agent        `yaml:"agents"`
	Furniture   []FurnitureDef `yaml:"furniture,omitempty"`

	// Dir is the absolute directory containing the blueprint file.
	// Used as the cwd for subprocesses (MCP servers, ACP agents) so that
	// relative paths in command/args resolve correctly regardless of where
	// `ofc` is invoked from. Set automatically by Load.
	Dir string `yaml:"-"`
}

// validateOAuth checks a furniture's oauth block, if any.
func validateOAuth(fd FurnitureDef) error {
	o := fd.OAuth
	if o == nil {
		return nil
	}
	if fd.Type != "mcp" || fd.URL == "" {
		return fmt.Errorf("oauth needs an MCP server reached by url")
	}
	switch o.Grant {
	case "", "authorization_code":
	case "client_credentials":
		if o.ClientID == "" || o.ClientSecret == "" {
			return fmt.Errorf("oauth grant client_credentials needs client_id and client_secret")
		}
	default:
		return fmt.Errorf("oauth grant %q: want authorization_code or client_credentials", o.Grant)
	}
	return nil
}

// disabled reports whether an entry's enabled field is set to false.
func disabled(enabled *bool) bool { return enabled != nil && !*enabled }

// decodeStrict decodes YAML into v; a key without a matching field is an
// error. An empty document decodes to the zero value.
func decodeStrict(data []byte, v any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(v); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// Load reads a blueprint from a YAML file
func Load(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var bp Blueprint
	if err := decodeStrict(data, &bp); err != nil {
		return nil, err
	}

	// Drop disabled entries first, so nothing below looks at them.
	bp.Agents = slices.DeleteFunc(bp.Agents, func(a Agent) bool { return disabled(a.Enabled) })
	bp.Furniture = slices.DeleteFunc(bp.Furniture, func(fd FurnitureDef) bool { return disabled(fd.Enabled) })

	// Expand ${VAR} references across the whole blueprint, before anything
	// else looks at a value. Runs here so it only ever sees inline YAML —
	// prompt_file contents are loaded below and are not subject to it.
	if err := expandBlueprintEnv(&bp); err != nil {
		return nil, err
	}

	// The name is the floor's ID: it appears in API URLs and scopes which
	// stored sessions belong to this floor.
	if !validName.MatchString(bp.Name) {
		return nil, fmt.Errorf("blueprint name %q must be non-empty and use only letters, digits, '.', '_' or '-': it identifies the floor in URLs", bp.Name)
	}
	for _, fd := range bp.Furniture {
		if err := validateOAuth(fd); err != nil {
			return nil, fmt.Errorf("furniture %q: %w", fd.Name, err)
		}
		if err := validateSandboxFields(fd); err != nil {
			return nil, fmt.Errorf("furniture %q: %w", fd.Name, err)
		}
	}

	// Resolve prompt files relative to blueprint directory, then expand templates
	bpDir := filepath.Dir(path)
	if absDir, err := filepath.Abs(bpDir); err == nil {
		bp.Dir = absDir
	} else {
		bp.Dir = bpDir
	}

	// Resolve agent refs: an entry with ref: is replaced wholesale by the
	// contents of the referenced file. Referenced files are not env-expanded.
	for i := range bp.Agents {
		if bp.Agents[i].Ref == "" {
			continue
		}
		p := bp.Agents[i].Ref
		if !filepath.IsAbs(p) {
			p = filepath.Join(bp.Dir, p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("agent[%d]: reading ref %q: %w", i, bp.Agents[i].Ref, err)
		}
		var a Agent
		if err := decodeStrict(data, &a); err != nil {
			return nil, fmt.Errorf("agent[%d]: parsing ref %q: %w", i, bp.Agents[i].Ref, err)
		}
		if a.Ref != "" {
			return nil, fmt.Errorf("agent[%d]: ref %q contains its own ref; chains are not supported", i, bp.Agents[i].Ref)
		}
		bp.Agents[i] = a
	}
	// A referenced file can disable its agent too.
	bp.Agents = slices.DeleteFunc(bp.Agents, func(a Agent) bool { return disabled(a.Enabled) })

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

		// Parse the prompt template now so syntax errors fail the load, and
		// keep it for rendering on each turn.
		bp.Agents[i].dir = bp.Dir
		if strings.Contains(bp.Agents[i].Prompt, "<%") {
			tmpl, err := parsePromptTemplate(bp.Agents[i].Prompt, bp.Dir)
			if err != nil {
				return nil, fmt.Errorf("agent %s: prompt template: %w", bp.Agents[i].ID, err)
			}
			bp.Agents[i].tmpl = tmpl
		}
	}

	// Resolve sandbox paths relative to the blueprint directory
	for i := range bp.Furniture {
		fd := &bp.Furniture[i]
		if fd.Dockerfile != "" && !filepath.IsAbs(fd.Dockerfile) {
			fd.Dockerfile = filepath.Join(bpDir, fd.Dockerfile)
		}
		if fd.Workspace != "" && !filepath.IsAbs(fd.Workspace) {
			fd.Workspace = filepath.Join(bpDir, fd.Workspace)
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
		if bp.Agents[i].APIKey == "" {
			bp.Agents[i].APIKey = bp.Defaults.APIKey
		}
		if bp.Agents[i].Thinking == "" {
			bp.Agents[i].Thinking = bp.Defaults.Thinking
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

	if err := validateAgentFurniture(&bp); err != nil {
		return nil, err
	}

	return &bp, nil
}

// validateSandboxFields checks that only sandbox furniture sets the
// sandbox fields.
func validateSandboxFields(fd FurnitureDef) error {
	if fd.Type == "sandbox" {
		return nil
	}
	if fd.Image != "" || fd.Dockerfile != "" || fd.Workspace != "" {
		return fmt.Errorf("image, dockerfile and workspace are fields of type sandbox, not %q", fd.Type)
	}
	return nil
}

// validateAgentFurniture checks that every furniture an agent lists
// exists, and that an agent's sandbox is a sandbox furniture of an ACP
// agent.
func validateAgentFurniture(bp *Blueprint) error {
	types := map[string]string{}
	for _, fd := range bp.Furniture {
		types[fd.Name] = fd.Type
	}
	for _, a := range bp.Agents {
		for _, name := range a.Furniture {
			if _, ok := types[name]; !ok {
				return fmt.Errorf("agent %s: furniture %q is not on the floor", a.ID, name)
			}
		}
		if a.Sandbox == "" {
			continue
		}
		if a.Type != "acp" {
			return fmt.Errorf("agent %s: sandbox is a field of ACP agents; an LLM agent lists the sandbox in furniture", a.ID)
		}
		if types[a.Sandbox] != "sandbox" {
			return fmt.Errorf("agent %s: sandbox %q is not a furniture of type sandbox", a.ID, a.Sandbox)
		}
	}
	return nil
}
