// Package floor implements the floor orchestration engine,
// managing multi-agent turn-taking, event routing, and frontends.
package floor

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// ANSI color codes
const (
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Red    = "\033[31m"
	Gray   = "\033[90m"
)

// agentColors is the palette cycled through for agent labels.
// @user always gets Cyan; agents get the rest in order.
var agentColors = []string{Green, Purple, Yellow, Blue, Red}

// ToolInteraction stores one tool call and its result.
type ToolInteraction struct {
	Command string
	Output  string
}

// Frame represents one level in the delegation chain.
// Caller asked Callee a question via @mention?
type Frame struct {
	Caller string // e.g. "@data"
	Callee string // e.g. "@code"
}

// Floor is the live DOM of agents and furniture for one or more Sessions.
// It owns the runtime agent set, furniture instances, sandbox, ACP subprocesses,
// and the API server — everything that's shared across conversations.
// Per-conversation state (chat log, agent memory, rooms) lives in Session.
//
// The Blueprint reference is the immutable template loaded from YAML.
// Mutations (AddAgent, AddFurniture, ...) modify Floor's own state — Agents,
// Furniture, etc. — not the Blueprint. Blueprint loading is simply a sequence
// of mutations applied during Start().
type Floor struct {
	Blueprint *blueprint.Blueprint // immutable template (the loaded YAML)

	// Live, mutable runtime state. Mutations modify these (under mu);
	// initial blueprint loading populates them via the same mutations.
	Agents          []blueprint.Agent
	Furniture       map[string]furniture.Furniture
	ACPSubprocesses map[string]*acpclient.Subprocess

	Sandbox   *sandbox.Sandbox
	APIServer *APIServer

	// Store persists session events (messages with per-agent visibility refs).
	// Defaults to NewMemoryStore() in NewFloor; can be overridden before
	// Start() for backed implementations (JSONL, SQL, etc.).
	Store SessionStore

	// Sessions on this Floor. In v1 there is always one default session
	// keyed as "default", created by NewFloor.
	Sessions map[string]*Session

	ListenAddr   string // API server listen address (default ":0" for auto)
	ServeWebDist bool   // serve web/dist/ as static files
	ExternalURL  string // override base URL in printed web UI link (for reverse proxies)
	DebugFunc    func(string)
	LogWriter    io.Writer
	StderrWriter io.Writer // where ACP subprocess stderr goes

	// mu serializes mutations to the live runtime state (Agents,
	// Furniture, ACPSubprocesses). v1 uses a plain mutex; if/when we
	// add a REST mutation API we can upgrade to a single-writer queue
	// without changing the method signatures.
	mu sync.Mutex
}

// NewFloor creates a Floor and applies the blueprint's static parts —
// LLM agents — immediately. Furniture and ACP agents are deferred to
// Start() because they require the API server to be up.
//
// Splitting application across NewFloor and Start lets tests use a Floor
// without spinning up infrastructure: LLM agents are ready, AgentContexts
// are populated, but no API server / sandbox / subprocesses are running.
// Both phases go through the same AddAgent / AddFurniture mutation
// primitives — there is no separate "blueprint loading" code path.
func NewFloor(bp *blueprint.Blueprint) *Floor {
	f := &Floor{
		Blueprint:       bp,
		Furniture:       make(map[string]furniture.Furniture),
		ACPSubprocesses: make(map[string]*acpclient.Subprocess),
		Store:           NewMemoryStore(),
		Sessions:        make(map[string]*Session),
		DebugFunc:       func(string) {},
	}
	f.Sessions["default"] = NewSession("default", f)

	// Apply LLM agents now. ACP agents need the API server and so wait
	// for Start(); furniture also needs the API server for MCP routes.
	for _, a := range bp.Agents {
		if a.Type == "acp" {
			continue
		}
		// AddAgent only fails for ACP-with-no-APIServer or duplicates;
		// neither applies here. Ignore error.
		_ = f.AddAgent(a, func(string) {})
	}

	return f
}

// DefaultSession returns the floor's default session.
// In v1 every floor has exactly one session, created at NewFloor time.
func (f *Floor) DefaultSession() *Session {
	return f.Sessions["default"]
}

// WorkspacePath returns the sandbox workspace directory, or "" if no sandbox.
func (f *Floor) WorkspacePath() string {
	if f.Sandbox != nil {
		return f.Sandbox.WorkspaceDir
	}
	return ""
}

// joinAgentIDs formats a list of agent IDs, excluding one (the agent itself).
func joinAgentIDs(ids []string, exclude string) string {
	var others []string
	for _, id := range ids {
		if id != exclude {
			others = append(others, id)
		}
	}
	if len(others) == 0 {
		return "no other agents"
	}
	return strings.Join(others, ", ")
}

// Start initializes API server, sandbox, then applies the blueprint as a
// sequence of mutations (AddFurniture, AddAgent). Same primitives that
// future runtime mutations will use.
func (f *Floor) Start(renderInfo func(string)) error {
	// 1. API server (always — serves floor message endpoints + furniture MCP)
	f.APIServer = NewAPIServer()

	// Generate auth token for web mode
	if f.ServeWebDist {
		token := GenerateToken()
		f.APIServer.SetAuthToken(token)
	}

	// Register floor API against the default session's chat.
	// (In v1 the API is session-scoped to default; multi-session URL paths come later.)
	f.APIServer.RegisterFloorAPI(f.DefaultSession().MainRoom, f.Blueprint, f.Furniture, f.WorkspacePath)

	// 2. Sandbox
	var sandboxWS *blueprint.Workstation
	for i := range f.Blueprint.Workstations {
		if f.Blueprint.Workstations[i].Type == "sandbox" {
			sandboxWS = &f.Blueprint.Workstations[i]
			break
		}
	}
	if sandboxWS != nil {
		workspaceDir := "./workspace"
		if sandboxWS.Mount != "" {
			// Mount format: "host:container" — use host part as workspace dir
			if parts := strings.SplitN(sandboxWS.Mount, ":", 2); len(parts) == 2 {
				workspaceDir = parts[0]
			}
		}
		f.Sandbox = sandbox.New(workspaceDir, sandboxWS.Image, sandboxWS.Dockerfile)
		renderInfo("Starting sandbox...")
		if err := f.Sandbox.Start(); err != nil {
			return fmt.Errorf("failed to start sandbox: %w", err)
		}
		renderInfo(fmt.Sprintf("Sandbox ready (%s)", f.Sandbox.ContainerID[:12]))
	}

	// 3. Serve web dist if enabled
	if f.ServeWebDist {
		webDir := findWebDist()
		if webDir != nil {
			f.APIServer.ServeStaticWeb(webDir)
			renderInfo("Web UI static files registered")
		} else {
			renderInfo("Warning: web/dist/ not found, web UI will not be served")
		}
	}

	// 4. Start API server. Routes can be added after Start() — both
	// AddFurniture (MCP routes) and the static handler register against
	// the running server. Echo serves whatever routes are registered
	// when a request comes in.
	listenAddr := f.ListenAddr
	if listenAddr == "" {
		listenAddr = ":0"
	}
	// Bind to localhost by default for security (token-authenticated API)
	if f.ServeWebDist && !strings.Contains(listenAddr, "127.0.0.1") && !strings.Contains(listenAddr, "localhost") {
		if strings.HasPrefix(listenAddr, ":") {
			listenAddr = "127.0.0.1" + listenAddr
		}
	}
	if err := f.APIServer.Start(listenAddr); err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	renderInfo(fmt.Sprintf("API server at %s", f.APIServer.BaseURL()))
	if f.ServeWebDist {
		baseURL := f.APIServer.BaseURL()
		if f.ExternalURL != "" {
			baseURL = strings.TrimSuffix(f.ExternalURL, "/")
		}
		token := f.APIServer.AuthToken()
		if token != "" {
			renderInfo(fmt.Sprintf("Web UI at %s?token=%s", baseURL, token))
		} else {
			renderInfo(fmt.Sprintf("Web UI at %s", baseURL))
		}
	}

	// 5. Apply the infrastructure-dependent parts of the blueprint via
	// mutations. LLM agents are already applied by NewFloor; here we
	// add furniture (needs API server for MCP routes) and ACP agents
	// (need API server for furniture MCP URLs).
	//
	// Furniture must come before ACP agents — ACP agents reference
	// furniture by name when registering their MCP server list.
	for _, fd := range f.Blueprint.Furniture {
		if err := f.AddFurniture(fd); err != nil {
			return fmt.Errorf("failed to add furniture %q: %w", fd.Name, err)
		}
		renderInfo(fmt.Sprintf("Furniture ready: %s (%s)", fd.Name, fd.Type))
	}
	for _, a := range f.Blueprint.Agents {
		if a.Type != "acp" {
			continue
		}
		if err := f.AddAgent(a, renderInfo); err != nil {
			return fmt.Errorf("failed to add agent %s: %w", a.ID, err)
		}
	}

	return nil
}

// Stop tears down ACP sessions, furniture, API server, and sandbox.
func (f *Floor) Stop() {
	for id, sub := range f.ACPSubprocesses {
		f.debug("closing ACP subprocess for %s", id)
		sub.Close()
	}
	if f.APIServer != nil {
		f.APIServer.Stop()
	}
	for _, fur := range f.Furniture {
		if closer, ok := fur.(io.Closer); ok {
			closer.Close()
		}
	}
	if f.Sandbox != nil {
		f.Sandbox.Stop()
	}
	for _, sess := range f.Sessions {
		sess.Close()
	}
}

// --- Mutation primitives ---
//
// These are the only ways to mutate Floor's live runtime state (Agents,
// Furniture, ACPSubprocesses). Blueprint loading in Start() uses the same
// primitives that future runtime callers (REST API, web UI) will use.
//
// All mutations are serialized by f.mu. Mutation methods emit no events
// today; when a Floor mutation event log lands (post-v1), the events will
// originate here.

// AddFurniture creates a furniture instance, wraps it for observability,
// and registers its MCP endpoints on the API server. Requires Start() to
// have run (the API server must be initialized).
func (f *Floor) AddFurniture(fd blueprint.FurnitureDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.APIServer == nil {
		return fmt.Errorf("AddFurniture(%q): Floor not started", fd.Name)
	}
	if _, exists := f.Furniture[fd.Name]; exists {
		return fmt.Errorf("furniture %q already exists", fd.Name)
	}

	fur, err := createFurniture(context.Background(), fd, f.Blueprint.Dir)
	if err != nil {
		return err
	}

	// Wrap so all Call() invocations emit FurnitureUpdated events on the
	// default session's chat. (v1: one session; later, per-session.)
	wrapped := &observableFurniture{inner: fur, chat: f.DefaultSession().MainRoom}
	f.Furniture[fd.Name] = wrapped

	// Register MCP endpoints on the running API server.
	f.APIServer.RegisterFurniture("default", fd.Name, furniture.WrapAsMCP(wrapped))

	return nil
}

// RemoveFurniture terminates the furniture (closing subprocesses if any)
// and removes it from the floor's furniture map. Best-effort: the API
// server's MCP routes for this furniture stay registered but will return
// 404 since the map lookup fails. Full route deregistration awaits a
// future API server refactor (single dispatcher route, see SESSIONS.md
// Step 8 area).
func (f *Floor) RemoveFurniture(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fur, ok := f.Furniture[name]
	if !ok {
		return fmt.Errorf("furniture %q not found", name)
	}

	delete(f.Furniture, name)

	if closer, ok := fur.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			f.debug("RemoveFurniture(%q): close error: %v", name, err)
		}
	}

	return nil
}

// AddAgent appends an agent to the floor and creates an AgentContext for
// it in every existing session. For ACP agents, also spawns the
// subprocess and registers its MCP server list — this requires Start()
// to have run (the API server must be up so ACP can reach furniture MCPs).
//
// renderInfo receives status updates during ACP subprocess startup. For
// non-ACP agents (LLM) it is unused. Pass a no-op for non-interactive
// callers.
func (f *Floor) AddAgent(spec blueprint.Agent, renderInfo func(string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, a := range f.Agents {
		if a.ID == spec.ID {
			return fmt.Errorf("agent %s already exists", spec.ID)
		}
	}
	if spec.Type == "acp" && f.APIServer == nil {
		return fmt.Errorf("AddAgent(%s): ACP agent requires Floor.Start() first", spec.ID)
	}

	// For ACP, spawn the subprocess before recording the agent — if startup
	// fails we don't want a half-registered agent.
	if spec.Type == "acp" {
		if err := f.spawnACPSubprocess(spec, renderInfo); err != nil {
			return err
		}
	}

	f.Agents = append(f.Agents, spec)
	for _, sess := range f.Sessions {
		sess.AddAgentContext(spec.ID)
	}
	return nil
}

// RemoveAgent removes an agent from the floor. For ACP agents, terminates
// the subprocess. Removes the AgentContext from every session.
func (f *Floor) RemoveAgent(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	idx := -1
	for i := range f.Agents {
		if f.Agents[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("agent %s not found", id)
	}

	// Terminate ACP subprocess if present.
	if sub, ok := f.ACPSubprocesses[id]; ok {
		f.debug("closing ACP subprocess for %s", id)
		sub.Close()
		delete(f.ACPSubprocesses, id)
	}

	f.Agents = append(f.Agents[:idx], f.Agents[idx+1:]...)
	for _, sess := range f.Sessions {
		sess.RemoveAgentContext(id)
	}
	return nil
}

// UpdateAgent applies in-place edits to an agent's config via the mutator
// function. Returns an error if the agent doesn't exist.
//
// Subprocess-affecting changes (Type, Command, Args, Env, Furniture list
// for ACP) take effect on the agent record but NOT on an already-running
// ACP subprocess. To rebuild the subprocess, callers must
// RemoveAgent + AddAgent. Hot-reloadable changes (Prompt, Model,
// Temperature, Endpoint, APIKey, Activation, ToolContext) take effect on
// the next agent turn — LLMAgent and ACPAgent read these fresh each Run.
func (f *Floor) UpdateAgent(id string, mutator func(*blueprint.Agent)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.Agents {
		if f.Agents[i].ID == id {
			mutator(&f.Agents[i])
			return nil
		}
	}
	return fmt.Errorf("agent %s not found", id)
}

// SetBlueprintMeta updates the floor's display metadata (name, description).
// Mutates the in-memory Blueprint reference. When persistence and blueprint
// snapshots land, this is the boundary between the immutable on-disk file
// and the mutable Floor state — for now, we accept the in-place mutation
// as a v1 simplification.
func (f *Floor) SetBlueprintMeta(name, description string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Blueprint.Name = name
	f.Blueprint.Description = description
}

// spawnACPSubprocess launches an ACP agent process, performs the ACP
// handshake, and stores the subprocess in f.ACPSubprocesses. Must be
// called with f.mu held.
func (f *Floor) spawnACPSubprocess(agent blueprint.Agent, renderInfo func(string)) error {
	if agent.Command == "" {
		return fmt.Errorf("ACP agent %s has no command configured", agent.ID)
	}

	renderInfo(fmt.Sprintf("Starting ACP agent %s (%s)...", agent.ID, agent.Command))

	cwd, _ := os.Getwd()
	workDir := filepath.Join(cwd, "workspace")
	os.MkdirAll(workDir, 0o755)
	client := acpclient.NewFloorClient(f.Sandbox, workDir)
	client.LogWriter = f.LogWriter
	client.DebugFunc = func(msg string) {
		renderInfo(msg)
	}

	stderrW := f.StderrWriter
	if stderrW == nil {
		stderrW = nil // will use os.Stderr in NewSubprocess
	}

	sub, err := acpclient.NewSubprocess(agent.Command, agent.Args, agent.Env, client, stderrW, f.Blueprint.Dir)
	if err != nil {
		return fmt.Errorf("failed to start ACP agent %s: %w", agent.ID, err)
	}

	ctx := context.Background()
	if err := sub.Initialize(ctx); err != nil {
		sub.Close()
		return fmt.Errorf("failed to initialize ACP agent %s: %w", agent.ID, err)
	}
	mcpServers := f.buildACPMCPServers(agent, sub)
	if err := sub.StartSession(ctx, workDir, mcpServers); err != nil {
		sub.Close()
		return fmt.Errorf("failed to create session for ACP agent %s: %w", agent.ID, err)
	}

	f.ACPSubprocesses[agent.ID] = sub
	renderInfo(fmt.Sprintf("ACP agent %s ready", agent.ID))
	return nil
}

// observableFurniture wraps a Furniture and emits FurnitureUpdated events
// on mutating Call()s. This covers all paths: LLM agents, ACP/MCP, and web UI.
type observableFurniture struct {
	inner furniture.Furniture
	chat  *Room
}

// readOnlyTools are tool names that don't mutate state (no need to notify).
// Covers both built-in (taskboard) and common external (filesystem MCP) tools.
var readOnlyTools = map[string]bool{
	"list_tasks":               true,
	"get_task":                 true,
	"list_directory":           true,
	"list_directory_with_sizes": true,
	"directory_tree":           true,
	"read_file":                true,
	"read_text_file":           true,
	"read_media_file":          true,
	"read_multiple_files":      true,
	"search_files":             true,
	"get_file_info":            true,
	"list_allowed_directories": true,
}

func (o *observableFurniture) Name() string             { return o.inner.Name() }
func (o *observableFurniture) Tools() []furniture.Tool   { return o.inner.Tools() }

// ReadFileRaw forwards to the inner furniture if it implements FileReader.
func (o *observableFurniture) ReadFileRaw(path string) ([]byte, string, error) {
	if fr, ok := o.inner.(furniture.FileReader); ok {
		return fr.ReadFileRaw(path)
	}
	return nil, "", fmt.Errorf("furniture %q does not support file reading", o.inner.Name())
}
func (o *observableFurniture) Call(toolName string, args map[string]interface{}) (interface{}, error) {
	result, err := o.inner.Call(toolName, args)
	if err == nil && !readOnlyTools[toolName] {
		o.chat.PostStream(FurnitureUpdated{Name: o.inner.Name()})
	}
	return result, err
}

// buildACPMCPServers builds the MCP server list for an ACP agent.
func (f *Floor) buildACPMCPServers(agent blueprint.Agent, session *acpclient.Subprocess) []acpsdk.McpServer {
	if f.APIServer == nil || len(agent.Furniture) == 0 {
		return nil
	}

	caps := session.McpCapabilities
	base := f.APIServer.BaseURL()

	// Include auth header if token is set
	var headers []acpsdk.HttpHeader
	if token := f.APIServer.AuthToken(); token != "" {
		headers = []acpsdk.HttpHeader{{Name: "Authorization", Value: "Bearer " + token}}
	}

	var servers []acpsdk.McpServer
	for _, fname := range agent.Furniture {
		if _, ok := f.Furniture[fname]; !ok {
			continue
		}

		switch {
		case caps.Sse:
			url := base + "/api/v1/floors/default/sse/" + fname
			servers = append(servers, acpsdk.McpServer{
				Sse: &acpsdk.McpServerSse{
					Type:    "sse",
					Name:    fname,
					Url:     url,
					Headers: headers,
				},
			})
		case caps.Http:
			url := base + "/api/v1/floors/default/mcp/" + fname + "/"
			servers = append(servers, acpsdk.McpServer{
				Http: &acpsdk.McpServerHttp{
					Type:    "http",
					Name:    fname,
					Url:     url,
					Headers: headers,
				},
			})
		default:
			f.debug("agent %s has no supported MCP transport for furniture %s", agent.ID, fname)
		}
	}
	return servers
}

func (f *Floor) debug(format string, args ...any) {
	if f.DebugFunc != nil {
		f.DebugFunc(fmt.Sprintf(format, args...))
	}
}

// createFurniture instantiates a furniture from its blueprint definition.
// bpDir is the absolute blueprint directory, used as cwd for stdio MCP subprocesses.
func createFurniture(ctx context.Context, fd blueprint.FurnitureDef, bpDir string) (furniture.Furniture, error) {
	switch fd.Type {
	case "taskboard":
		return furniture.NewTaskBoard(), nil
	case "mcp":
		if fd.URL != "" {
			return furniture.NewExternalMCPFromURL(ctx, fd.Name, fd.URL, fd.Headers)
		}
		if fd.Command == "" {
			return nil, fmt.Errorf("mcp furniture %q requires a command or url", fd.Name)
		}
		return furniture.NewExternalMCP(ctx, fd.Name, fd.Command, fd.Args, bpDir)
	default:
		return nil, fmt.Errorf("unknown furniture type %q", fd.Type)
	}
}

// findWebDist locates the web/dist/ directory relative to the executable.
// Tries: ./web/dist, then relative to the executable binary.
func findWebDist() fs.FS {
	// Try relative to cwd first
	if info, err := os.Stat("web/dist"); err == nil && info.IsDir() {
		return os.DirFS("web/dist")
	}

	// Try relative to executable
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Join(filepath.Dir(exe), "..", "web", "dist")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return os.DirFS(dir)
		}
	}

	return nil
}

// BuildColorMap assigns colors to agents, cycling through the palette.
func BuildColorMap(bp *blueprint.Blueprint) map[string]string {
	cm := map[string]string{"@user": Cyan}
	for i, a := range bp.Agents {
		cm[a.ID] = agentColors[i%len(agentColors)]
	}
	return cm
}
