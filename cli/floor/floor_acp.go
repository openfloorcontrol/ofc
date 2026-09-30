package floor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	acpsdk "github.com/coder/acp-go-sdk"
	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
)

// acpKey identifies one ACP subprocess: each agent gets its own process
// in each session, since the process holds the agent's conversation.
type acpKey struct {
	session string
	agent   string
}

// acpSubprocess returns the ACP subprocess for agentID in sessionID,
// spawning it on the agent's first turn in that session.
func (f *Floor) acpSubprocess(sessionID, agentID string) (*acpclient.Subprocess, error) {
	// Lock order is mu before acpMu (RemoveAgent holds mu while closing),
	// so the spec is read before taking acpMu.
	spec, ok := f.agentSpec(agentID)
	if !ok {
		return nil, fmt.Errorf("unknown agent %s", agentID)
	}

	f.acpMu.Lock()
	defer f.acpMu.Unlock()

	key := acpKey{session: sessionID, agent: agentID}
	if sub, ok := f.acpSubprocesses[key]; ok {
		return sub, nil
	}
	sub, err := f.spawnACPSubprocess(spec)
	if err != nil {
		return nil, err
	}
	f.acpSubprocesses[key] = sub
	return sub, nil
}

// closeACPSubprocesses closes the subprocesses whose key matches.
func (f *Floor) closeACPSubprocesses(match func(acpKey) bool) {
	f.acpMu.Lock()
	defer f.acpMu.Unlock()
	for key, sub := range f.acpSubprocesses {
		if match(key) {
			f.debug("closing ACP subprocess for %s in session %s", key.agent, key.session)
			sub.Close()
			delete(f.acpSubprocesses, key)
		}
	}
}

// agentSpec returns the live spec of an agent on the floor.
func (f *Floor) agentSpec(agentID string) (blueprint.Agent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.agents {
		if a.ID == agentID {
			return a, true
		}
	}
	return blueprint.Agent{}, false
}

// spawnACPSubprocess launches an ACP agent process, performs the ACP
// handshake, and opens an ACP session in it.
func (f *Floor) spawnACPSubprocess(agent blueprint.Agent) (*acpclient.Subprocess, error) {
	if agent.Command == "" {
		return nil, fmt.Errorf("ACP agent %s has no command configured", agent.ID)
	}
	if f.APIServer == nil {
		return nil, fmt.Errorf("ACP agent %s needs Floor.Start() first", agent.ID)
	}

	f.debug("starting ACP agent %s (%s)", agent.ID, agent.Command)

	cwd, _ := os.Getwd()
	workDir := filepath.Join(cwd, "workspace")
	os.MkdirAll(workDir, 0o755)
	client := acpclient.NewFloorClient(f.Sandbox, workDir)
	client.LogWriter = f.LogWriter
	client.DebugFunc = func(msg string) { f.debug("%s", msg) }

	sub, err := acpclient.NewSubprocess(agent.Command, agent.Args, agent.Env, client, f.StderrWriter, f.Blueprint.Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to start ACP agent %s: %w", agent.ID, err)
	}

	ctx := context.Background()
	if err := sub.Initialize(ctx); err != nil {
		sub.Close()
		return nil, fmt.Errorf("failed to initialize ACP agent %s: %w", agent.ID, err)
	}
	mcpServers := f.buildACPMCPServers(agent, sub)
	if err := sub.StartSession(ctx, workDir, mcpServers); err != nil {
		sub.Close()
		return nil, fmt.Errorf("failed to create session for ACP agent %s: %w", agent.ID, err)
	}

	f.debug("ACP agent %s ready", agent.ID)
	return sub, nil
}

// buildACPMCPServers builds the MCP server list for an ACP agent — the
// URLs the agent should connect to for each furniture it has access to.
// Picks SSE or HTTP transport based on what the agent advertises in
// its initialization response.
func (f *Floor) buildACPMCPServers(agent blueprint.Agent, session *acpclient.Subprocess) []acpsdk.McpServer {
	if f.APIServer == nil || len(agent.Furniture) == 0 {
		return nil
	}

	caps := session.McpCapabilities
	base := f.APIServer.BaseURL()
	floorID := f.ID()

	// Include auth header if token is set. Init as empty slice (not nil)
	// so it serializes to JSON `[]` instead of `null` — claude-code-acp's
	// schema rejects null for the headers field.
	headers := []acpsdk.HttpHeader{}
	if token := f.APIServer.AuthToken(); token != "" {
		headers = append(headers, acpsdk.HttpHeader{Name: "Authorization", Value: "Bearer " + token})
	}

	var servers []acpsdk.McpServer
	for _, fname := range agent.Furniture {
		if _, ok := f.Furniture[fname]; !ok {
			continue
		}

		switch {
		case caps.Sse:
			url := base + "/api/v1/floors/" + floorID + "/sse/" + fname
			servers = append(servers, acpsdk.McpServer{
				Sse: &acpsdk.McpServerSseInline{
					Type:    "sse",
					Name:    fname,
					Url:     url,
					Headers: headers,
				},
			})
		case caps.Http:
			url := base + "/api/v1/floors/" + floorID + "/mcp/" + fname + "/"
			servers = append(servers, acpsdk.McpServer{
				Http: &acpsdk.McpServerHttpInline{
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
