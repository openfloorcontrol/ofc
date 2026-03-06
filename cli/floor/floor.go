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

// Floor is the shared space: Chat + Furniture + infrastructure.
// It doesn't know about AI or turn-taking — that's the Controller's job.
type Floor struct {
	Chat          *Chat
	Blueprint     *blueprint.Blueprint
	Sandbox       *sandbox.Sandbox
	Furniture     map[string]furniture.Furniture
	APIServer     *APIServer
	ACPSessions   map[string]*acpclient.AgentSession
	AgentContexts map[string]*AgentContext // per-agent context accumulators
	Rooms         map[string]*Room         // active rooms by ID
	agentRoom     map[string]string        // agentID → roomID ("" = main floor)
	unified       chan TaggedEvent          // merged event channel (lazy, set by StartUnified)
	ListenAddr    string                   // API server listen address (default ":0" for auto)
	ServeWebDist  bool                     // serve web/dist/ as static files
	DebugFunc     func(string)
	LogWriter     io.Writer
	StderrWriter  io.Writer // where ACP subprocess stderr goes
}

// NewFloor creates a Floor from a blueprint.
// Creates AgentContexts for all agents and registers them as Chat listeners.
func NewFloor(bp *blueprint.Blueprint) *Floor {
	chat := NewChat()
	agentContexts := make(map[string]*AgentContext, len(bp.Agents))
	for _, agent := range bp.Agents {
		ac := NewAgentContext(agent.ID)
		agentContexts[agent.ID] = ac
		chat.AddListener(ac)
	}

	return &Floor{
		Chat:          chat,
		Blueprint:     bp,
		Furniture:     make(map[string]furniture.Furniture),
		ACPSessions:   make(map[string]*acpclient.AgentSession),
		AgentContexts: agentContexts,
		Rooms:         make(map[string]*Room),
		agentRoom:     make(map[string]string),
		DebugFunc:     func(string) {},
	}
}

// GetAgentContext returns the context for the given agent, or nil if not found.
func (f *Floor) GetAgentContext(agentID string) *AgentContext {
	return f.AgentContexts[agentID]
}

// AgentRoom returns the room ID an agent is in ("" = main floor).
func (f *Floor) AgentRoom(agentID string) string {
	return f.agentRoom[agentID]
}

// CreateRoom creates an isolated sub-conversation room.
// Moves the specified agents' listeners from their current Chat to the room's Chat,
// and inserts system messages into each agent's context about the transition.
func (f *Floor) CreateRoom(roomID, creator string, agentIDs []string, prompt string) (*Room, error) {
	if _, exists := f.Rooms[roomID]; exists {
		return nil, fmt.Errorf("room %s already exists", roomID)
	}

	// Validate all agents exist
	for _, aid := range agentIDs {
		if _, ok := f.AgentContexts[aid]; !ok {
			return nil, fmt.Errorf("unknown agent %s", aid)
		}
		if existing := f.agentRoom[aid]; existing != "" {
			return nil, fmt.Errorf("agent %s is already in room %s", aid, existing)
		}
	}

	room := NewRoom(roomID, creator, agentIDs, prompt, f.Blueprint)
	f.Rooms[roomID] = room

	// Build participant list for system message
	var participantNames []string
	for _, aid := range agentIDs {
		participantNames = append(participantNames, aid)
	}

	// Move agents: switch listeners from current Chat to room Chat
	for _, aid := range agentIDs {
		ac := f.AgentContexts[aid]

		// Remove listener from main floor Chat
		f.Chat.RemoveListener(ac)

		// Add listener to room Chat
		room.Chat.AddListener(ac)

		// Insert system message about the room transition
		ac.AppendSystem(fmt.Sprintf("You moved to room %s with %s. Messages here are private to this room.",
			roomID, joinAgentIDs(participantNames, aid)))

		f.agentRoom[aid] = roomID
	}

	// Start forwarding room events to unified channel (if active)
	if f.unified != nil {
		go f.forwardRoomEvents(room)
	}

	f.debug("created room %s with agents %v", roomID, agentIDs)
	return room, nil
}

// CloseRoom closes a room and moves agents back to the main floor.
// Posts a summary to the main floor Chat.
func (f *Floor) CloseRoom(roomID string) error {
	room, ok := f.Rooms[roomID]
	if !ok {
		return fmt.Errorf("room %s not found", roomID)
	}
	if room.IsClosed() {
		return fmt.Errorf("room %s is already closed", roomID)
	}

	// Build summary from room history
	history := room.Chat.History()
	var summary string
	if len(history) > 0 {
		last := history[len(history)-1]
		summary = fmt.Sprintf("[Room %s closed] Last message from %s: %s", roomID, last.From, last.Content)
	} else {
		summary = fmt.Sprintf("[Room %s closed] No messages were exchanged.", roomID)
	}

	// Move agents back to main floor
	for aid := range room.AgentIDs {
		ac := f.AgentContexts[aid]

		// Remove listener from room Chat
		room.Chat.RemoveListener(ac)

		// Add listener back to main floor Chat
		f.Chat.AddListener(ac)

		// Insert system message about returning
		ac.AppendSystem(fmt.Sprintf("Room %s closed. You are back on the main floor.", roomID))

		delete(f.agentRoom, aid)
	}

	// Close the room (stops its Chat, terminates event forwarding)
	room.Close(summary)

	// Post summary to main floor
	f.Chat.Post(ChatMessage{
		From:    "@system",
		Content: summary,
	})

	f.debug("closed room %s", roomID)
	return nil
}

// ViewForRoom returns a lightweight Floor copy where Chat points to the room's Chat.
// Agents running in a room use this so their Chat.Post() goes to the room.
// Shares Sandbox, Furniture, ACPSessions, and AgentContexts with the parent.
func (f *Floor) ViewForRoom(room *Room) *Floor {
	return &Floor{
		Chat:          room.Chat,
		Blueprint:     f.Blueprint,
		Sandbox:       f.Sandbox,
		Furniture:     f.Furniture,
		ACPSessions:   f.ACPSessions,
		AgentContexts: f.AgentContexts,
		Rooms:         f.Rooms,
		agentRoom:     f.agentRoom,
		DebugFunc:     f.DebugFunc,
		LogWriter:     f.LogWriter,
		StderrWriter:  f.StderrWriter,
	}
}

// StartUnified creates a merged event channel that receives events from
// the main floor Chat and all active rooms. Returns the channel.
// Room events are tagged with their RoomID; main floor events have RoomID "".
func (f *Floor) StartUnified() <-chan TaggedEvent {
	f.unified = make(chan TaggedEvent, 64)

	// Forward main floor events
	go func() {
		for ev := range f.Chat.Events() {
			f.unified <- TaggedEvent{RoomID: "", Event: ev}
		}
		close(f.unified)
	}()

	return f.unified
}

// forwardRoomEvents forwards events from a room's Chat to the unified channel.
// Terminates when the room's Chat is closed.
func (f *Floor) forwardRoomEvents(room *Room) {
	for ev := range room.Chat.Events() {
		if f.unified != nil {
			f.unified <- TaggedEvent{RoomID: room.ID, Event: ev}
		}
	}
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

	// Register floor API (furniture map is populated later but closures capture the reference)
	f.APIServer.RegisterFloorAPI(f.Chat, f.Blueprint, f.Furniture)

	// 2. Sandbox
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
	if err := f.APIServer.Start(listenAddr); err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	renderInfo(fmt.Sprintf("API server at %s", f.APIServer.BaseURL()))
	if f.ServeWebDist {
		renderInfo(fmt.Sprintf("Web UI at %s", f.APIServer.BaseURL()))
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

// initFurniture creates furniture instances and registers them on the API server.
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

	// Register furniture MCP endpoints on the existing API server
	for name, fur := range f.Furniture {
		mcpSrv := furniture.WrapAsMCP(fur)
		f.APIServer.RegisterFurniture("default", name, mcpSrv)
	}

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
