package floor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// Coordinator wires the controller, runners, and frontend together.
// It owns the lifecycle (sandbox, ACP sessions) and the main loop.
type Coordinator struct {
	ctrl     *Controller
	frontend *CLIFrontend
	sandbox  *sandbox.Sandbox
	sessions map[string]*acpclient.AgentSession
	bp       *blueprint.Blueprint
	colorMap map[string]string
}

// NewCoordinator creates a coordinator with a CLI frontend.
// This is the main entry point for running a floor from the command line.
func NewCoordinator(bp *blueprint.Blueprint, debug bool, logPath string) *Coordinator {
	// Assign colors to agents
	cm := map[string]string{"@user": Cyan}
	for i, a := range bp.Agents {
		cm[a.ID] = agentColors[i%len(agentColors)]
	}

	frontend := NewCLIFrontend(logPath, debug, cm)

	ctrl := NewController(bp)
	ctrl.DebugFunc = frontend.Debug

	return &Coordinator{
		ctrl:     ctrl,
		frontend: frontend,
		bp:       bp,
		colorMap: cm,
		sessions: make(map[string]*acpclient.AgentSession),
	}
}

// Start initializes sandbox and ACP agent sessions.
func (co *Coordinator) Start() error {
	// Start sandbox if one is defined in the blueprint
	var sandboxWS *blueprint.Workstation
	for i := range co.bp.Workstations {
		if co.bp.Workstations[i].Type == "sandbox" {
			sandboxWS = &co.bp.Workstations[i]
			break
		}
	}

	if sandboxWS != nil {
		co.sandbox = sandbox.New("./workspace", sandboxWS.Image, sandboxWS.Dockerfile)
		co.frontend.Render(SystemInfo{Text: "Starting sandbox..."})
		if err := co.sandbox.Start(); err != nil {
			return fmt.Errorf("failed to start sandbox: %w", err)
		}
		co.frontend.Render(SystemInfo{Text: fmt.Sprintf("Sandbox ready (%s)", co.sandbox.ContainerID[:12])})
	}

	// Launch ACP agent sessions
	for _, agent := range co.bp.Agents {
		if agent.Type != "acp" {
			continue
		}
		if agent.Command == "" {
			return fmt.Errorf("ACP agent %s has no command configured", agent.ID)
		}

		co.frontend.Render(SystemInfo{Text: fmt.Sprintf("Starting ACP agent %s (%s)...", agent.ID, agent.Command)})

		cwd, _ := os.Getwd()
		workDir := filepath.Join(cwd, "workspace")
		os.MkdirAll(workDir, 0o755)
		client := acpclient.NewFloorClient(co.sandbox, workDir, co.frontend.IsDebug())
		client.LogWriter = co.frontend.LogWriter()
		session, err := acpclient.NewAgentSession(agent.Command, agent.Args, agent.Env, client)
		if err != nil {
			return fmt.Errorf("failed to start ACP agent %s: %w", agent.ID, err)
		}

		ctx := context.Background()
		if err := session.Initialize(ctx); err != nil {
			session.Close()
			return fmt.Errorf("failed to initialize ACP agent %s: %w", agent.ID, err)
		}
		if err := session.StartSession(ctx, workDir); err != nil {
			session.Close()
			return fmt.Errorf("failed to create session for ACP agent %s: %w", agent.ID, err)
		}

		co.sessions[agent.ID] = session
		co.frontend.Render(SystemInfo{Text: fmt.Sprintf("ACP agent %s ready", agent.ID)})
	}

	return nil
}

// Stop tears down ACP sessions and sandbox.
func (co *Coordinator) Stop() {
	for id, session := range co.sessions {
		co.frontend.Debug(fmt.Sprintf("closing ACP session for %s", id))
		session.Close()
	}
	if co.sandbox != nil {
		co.sandbox.Stop()
	}
}

// Run is the main loop.
func (co *Coordinator) Run(initialPrompt string) error {
	if err := co.Start(); err != nil {
		return err
	}
	defer co.Stop()
	defer co.frontend.Close()

	// Print header
	co.renderHeader()

	// One-shot mode: process initial prompt and return
	if initialPrompt != "" {
		co.renderInitialPrompt(initialPrompt)
		co.processEvents(co.ctrl.HandleEvent(UserMessage{Content: initialPrompt}))
		return nil
	}

	// Interactive loop
	for {
		ev, err := co.frontend.ReadInput()
		if err != nil {
			break // EOF / interrupt
		}

		events := co.ctrl.HandleEvent(ev)
		stopped := co.processEvents(events)
		if stopped {
			break
		}
	}

	return nil
}

// processEvents handles events from the controller.
// Returns true if the floor should stop.
func (co *Coordinator) processEvents(events []Event) bool {
	for _, ev := range events {
		co.frontend.Render(ev)

		switch e := ev.(type) {
		case PromptAgent:
			// Dispatch to runner, get result, feed back into controller
			co.frontend.Render(AgentThinking{AgentID: e.AgentID})
			result := co.runAgent(e.AgentID)
			co.frontend.Render(result.Event)
			// Feed the result into the controller and process recursively
			if stopped := co.processEvents(co.ctrl.HandleEvent(result.Event)); stopped {
				return true
			}
		case FloorStopped:
			return true
		}
	}
	return false
}

// runAgent dispatches to the right runner.
func (co *Coordinator) runAgent(agentID string) RunnerResult {
	agent := co.ctrl.getAgent(agentID)
	if agent == nil {
		return RunnerResult{Event: AgentError{
			AgentID: agentID,
			Err:     fmt.Errorf("unknown agent %s", agentID),
		}}
	}

	if agent.Type == "acp" {
		runner := &ACPRunner{
			Sessions: co.sessions,
			Stream:   co.frontend,
		}
		blocks := co.ctrl.BuildACPContext(agent)
		co.frontend.Debug(fmt.Sprintf("ACP prompt for %s (%d blocks)", agent.ID, len(blocks)))
		return runner.Run(agent, blocks)
	}

	// Default: LLM runner
	runner := &LLMRunner{
		Sandbox: co.sandbox,
		Stream:  co.frontend,
	}
	messages := co.ctrl.BuildContext(agent)
	return runner.Run(agent, messages)
}

// renderHeader prints the floor header.
func (co *Coordinator) renderHeader() {
	co.frontend.Render(SystemInfo{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
	co.frontend.Render(SystemInfo{Text: fmt.Sprintf("%sOFC - %s%s", Bold, co.bp.Name, Reset)})
	if co.bp.Description != "" {
		co.frontend.Render(SystemInfo{Text: co.bp.Description})
	}

	var agentList []string
	for _, a := range co.bp.Agents {
		color := Cyan
		if c, ok := co.colorMap[a.ID]; ok {
			color = c
		}
		agentList = append(agentList, color+a.ID+Reset)
	}
	co.frontend.Render(SystemInfo{Text: fmt.Sprintf("Agents: %s", strings.Join(agentList, ", "))})
	co.frontend.Render(SystemInfo{Text: fmt.Sprintf("Type %s/quit%s to exit, %s/clear%s to reset", Bold, Reset, Bold, Reset)})
	co.frontend.Render(SystemInfo{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
}

// renderInitialPrompt displays the initial prompt as if the user typed it.
func (co *Coordinator) renderInitialPrompt(prompt string) {
	co.frontend.OnStream(AgentLabel{AgentID: "@user"})
	co.frontend.OnStream(TokenStreamed{AgentID: "@user", Token: prompt + "\n"})
}
