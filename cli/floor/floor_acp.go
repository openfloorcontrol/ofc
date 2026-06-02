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
