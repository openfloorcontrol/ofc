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

// Floor is the long-lived shared space for one or more Sessions.
// It owns furniture instances, sandbox, ACP subprocesses, and the API server —
// everything that's shared across conversations on the same blueprint instance.
// Per-conversation state (chat log, agent memory, rooms) lives in Session.
type Floor struct {
	Blueprint       *blueprint.Blueprint
	Sandbox         *sandbox.Sandbox
	Furniture       map[string]furniture.Furniture
	APIServer       *APIServer
	ACPSubprocesses map[string]*acpclient.Subprocess

	// Sessions on this Floor. In v1 there is always one default session
	// keyed as "default", created by NewFloor.
	Sessions map[string]*Session

	ListenAddr   string // API server listen address (default ":0" for auto)
	ServeWebDist bool   // serve web/dist/ as static files
	ExternalURL  string // override base URL in printed web UI link (for reverse proxies)
	DebugFunc    func(string)
	LogWriter    io.Writer
	StderrWriter io.Writer // where ACP subprocess stderr goes
}

// NewFloor creates a Floor from a blueprint and a default Session.
func NewFloor(bp *blueprint.Blueprint) *Floor {
	f := &Floor{
		Blueprint:       bp,
		Furniture:       make(map[string]furniture.Furniture),
		ACPSubprocesses: make(map[string]*acpclient.Subprocess),
		Sessions:        make(map[string]*Session),
		DebugFunc:       func(string) {},
	}
	f.Sessions["default"] = NewSession("default", f)
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

// Start initializes API server, sandbox, furniture, and ACP sessions.
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
	f.APIServer.RegisterFloorAPI(f.DefaultSession().Chat, f.Blueprint, f.Furniture, f.WorkspacePath)

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

	// 3. Furniture
	if err := f.initFurniture(renderInfo); err != nil {
		return err
	}

	// 4. Serve web dist if enabled
	if f.ServeWebDist {
		webDir := findWebDist()
		if webDir != nil {
			f.APIServer.ServeStaticWeb(webDir)
			renderInfo("Web UI static files registered")
		} else {
			renderInfo("Warning: web/dist/ not found, web UI will not be served")
		}
	}

	// 5. Start API server (after furniture is registered)
	listenAddr := f.ListenAddr
	if listenAddr == "" {
		listenAddr = ":0"
	}
	// Bind to localhost by default for security (token-authenticated API)
	if f.ServeWebDist && !strings.Contains(listenAddr, "127.0.0.1") && !strings.Contains(listenAddr, "localhost") {
		// Replace ":port" with "127.0.0.1:port"
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

	// 6. ACP agent sessions
	for _, agent := range f.Blueprint.Agents {
		if agent.Type != "acp" {
			continue
		}
		if err := f.startACPAgent(agent, renderInfo); err != nil {
			return err
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

// initFurniture creates furniture instances and registers them on the API server.
func (f *Floor) initFurniture(renderInfo func(string)) error {
	if len(f.Blueprint.Furniture) == 0 {
		return nil
	}

	ctx := context.Background()
	for _, fd := range f.Blueprint.Furniture {
		fur, err := createFurniture(ctx, fd, f.Blueprint.Dir)
		if err != nil {
			return fmt.Errorf("failed to create furniture %q: %w", fd.Name, err)
		}
		// Wrap so all Call() invocations emit FurnitureUpdated events on the
		// default session's chat. (v1: one session; later, observability
		// will route per-session.)
		fur = &observableFurniture{inner: fur, chat: f.DefaultSession().Chat}
		f.Furniture[fd.Name] = fur
		renderInfo(fmt.Sprintf("Furniture ready: %s (%s)", fd.Name, fd.Type))
	}

	// Register furniture MCP endpoints on the existing API server
	for name, fur := range f.Furniture {
		mcpSrv := furniture.WrapAsMCP(fur)
		f.APIServer.RegisterFurniture("default", name, mcpSrv)
	}

	return nil
}

// observableFurniture wraps a Furniture and emits FurnitureUpdated events
// on mutating Call()s. This covers all paths: LLM agents, ACP/MCP, and web UI.
type observableFurniture struct {
	inner furniture.Furniture
	chat  *Chat
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

// startACPAgent initializes one ACP agent session.
func (f *Floor) startACPAgent(agent blueprint.Agent, renderInfo func(string)) error {
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

	session, err := acpclient.NewSubprocess(agent.Command, agent.Args, agent.Env, client, stderrW, f.Blueprint.Dir)
	if err != nil {
		return fmt.Errorf("failed to start ACP agent %s: %w", agent.ID, err)
	}

	ctx := context.Background()
	if err := session.Initialize(ctx); err != nil {
		session.Close()
		return fmt.Errorf("failed to initialize ACP agent %s: %w", agent.ID, err)
	}
	mcpServers := f.buildACPMCPServers(agent, session)
	if err := session.StartSession(ctx, workDir, mcpServers); err != nil {
		session.Close()
		return fmt.Errorf("failed to create session for ACP agent %s: %w", agent.ID, err)
	}

	f.ACPSubprocesses[agent.ID] = session
	renderInfo(fmt.Sprintf("ACP agent %s ready", agent.ID))
	return nil
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
