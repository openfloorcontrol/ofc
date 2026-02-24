// Package floor implements the floor orchestration engine,
// managing multi-agent turn-taking, event routing, and frontends.
package floor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

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

// Floor is the shared space: Chat + Furniture + infrastructure.
// It doesn't know about AI or turn-taking — that's the Controller's job.
type Floor struct {
	Chat         *Chat
	Blueprint    *blueprint.Blueprint
	Sandbox      *sandbox.Sandbox
	Furniture    map[string]furniture.Furniture
	APIServer    *APIServer
	ACPSessions  map[string]*acpclient.AgentSession
	DebugFunc    func(string)
	LogWriter    io.Writer
	StderrWriter io.Writer // where ACP subprocess stderr goes
}

// NewFloor creates a Floor from a blueprint.
func NewFloor(bp *blueprint.Blueprint) *Floor {
	return &Floor{
		Chat:        NewChat(),
		Blueprint:   bp,
		Furniture:   make(map[string]furniture.Furniture),
		ACPSessions: make(map[string]*acpclient.AgentSession),
		DebugFunc:   func(string) {},
	}
}

// Start initializes sandbox, furniture, and ACP sessions.
func (f *Floor) Start(renderInfo func(string)) error {
	// 1. Sandbox
	var sandboxWS *blueprint.Workstation
	for i := range f.Blueprint.Workstations {
		if f.Blueprint.Workstations[i].Type == "sandbox" {
			sandboxWS = &f.Blueprint.Workstations[i]
			break
		}
	}
	if sandboxWS != nil {
		f.Sandbox = sandbox.New("./workspace", sandboxWS.Image, sandboxWS.Dockerfile)
		renderInfo("Starting sandbox...")
		if err := f.Sandbox.Start(); err != nil {
			return fmt.Errorf("failed to start sandbox: %w", err)
		}
		renderInfo(fmt.Sprintf("Sandbox ready (%s)", f.Sandbox.ContainerID[:12]))
	}

	// 2. Furniture
	if err := f.initFurniture(renderInfo); err != nil {
		return err
	}

	// 3. ACP agent sessions
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
	for id, session := range f.ACPSessions {
		f.debug("closing ACP session for %s", id)
		session.Close()
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
	f.Chat.Close()
}

// initFurniture creates furniture instances and starts the API server.
func (f *Floor) initFurniture(renderInfo func(string)) error {
	if len(f.Blueprint.Furniture) == 0 {
		return nil
	}

	ctx := context.Background()
	for _, fd := range f.Blueprint.Furniture {
		fur, err := createFurniture(ctx, fd)
		if err != nil {
			return fmt.Errorf("failed to create furniture %q: %w", fd.Name, err)
		}
		f.Furniture[fd.Name] = fur
		renderInfo(fmt.Sprintf("Furniture ready: %s (%s)", fd.Name, fd.Type))
	}

	// Start API server for MCP access
	f.APIServer = NewAPIServer()
	for name, fur := range f.Furniture {
		mcpSrv := furniture.WrapAsMCP(fur)
		f.APIServer.RegisterFurniture("default", name, mcpSrv)
	}
	if err := f.APIServer.Start(":0"); err != nil {
		return fmt.Errorf("failed to start furniture API server: %w", err)
	}
	renderInfo(fmt.Sprintf("Furniture API server at %s", f.APIServer.BaseURL()))

	return nil
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
		stderrW = nil // will use os.Stderr in NewAgentSession
	}

	session, err := acpclient.NewAgentSession(agent.Command, agent.Args, agent.Env, client, stderrW)
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

	f.ACPSessions[agent.ID] = session
	renderInfo(fmt.Sprintf("ACP agent %s ready", agent.ID))
	return nil
}

// buildACPMCPServers builds the MCP server list for an ACP agent.
func (f *Floor) buildACPMCPServers(agent blueprint.Agent, session *acpclient.AgentSession) []acpsdk.McpServer {
	if f.APIServer == nil || len(agent.Furniture) == 0 {
		return nil
	}

	caps := session.McpCapabilities
	base := f.APIServer.BaseURL()

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
					Headers: []acpsdk.HttpHeader{},
				},
			})
		case caps.Http:
			url := base + "/api/v1/floors/default/mcp/" + fname + "/"
			servers = append(servers, acpsdk.McpServer{
				Http: &acpsdk.McpServerHttp{
					Type:    "http",
					Name:    fname,
					Url:     url,
					Headers: []acpsdk.HttpHeader{},
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
func createFurniture(ctx context.Context, fd blueprint.FurnitureDef) (furniture.Furniture, error) {
	switch fd.Type {
	case "taskboard":
		return furniture.NewTaskBoard(), nil
	case "mcp":
		if fd.Command == "" {
			return nil, fmt.Errorf("mcp furniture %q requires a command", fd.Name)
		}
		return furniture.NewExternalMCP(ctx, fd.Name, fd.Command, fd.Args)
	default:
		return nil, fmt.Errorf("unknown furniture type %q", fd.Type)
	}
}

// BuildColorMap assigns colors to agents, cycling through the palette.
func BuildColorMap(bp *blueprint.Blueprint) map[string]string {
	cm := map[string]string{"@user": Cyan}
	for i, a := range bp.Agents {
		cm[a.ID] = agentColors[i%len(agentColors)]
	}
	return cm
}
