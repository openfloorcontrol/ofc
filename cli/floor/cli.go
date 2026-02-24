package floor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// CLIFrontend provides terminal-based interaction for the floor.
type CLIFrontend struct {
	out      *Output
	colorMap map[string]string
	reader   *bufio.Reader
}

// NewCLIFrontend creates a CLI frontend with terminal output and optional log file.
func NewCLIFrontend(logPath string, debug bool, colorMap map[string]string) *CLIFrontend {
	return &CLIFrontend{
		out:      NewOutput(logPath, debug),
		colorMap: colorMap,
		reader:   bufio.NewReader(os.Stdin),
	}
}

func (f *CLIFrontend) agentColor(id string) string {
	if c, ok := f.colorMap[id]; ok {
		return c
	}
	return Cyan
}

// LogWriter returns the log file writer for subsystems (ACP client debug).
func (f *CLIFrontend) LogWriter() io.Writer {
	return f.out.LogWriter()
}

// Close closes the log file.
func (f *CLIFrontend) Close() {
	f.out.Close()
}

// IsDebug returns whether debug mode is enabled.
func (f *CLIFrontend) IsDebug() bool {
	return f.out.debug
}

// Debug writes a debug message (grey on terminal, plain in log).
func (f *CLIFrontend) Debug(msg string) {
	f.out.Debug("%s", msg)
}

// RunLoop is the event-driven main loop for CLI.
// It reads from Chat.Events(), renders events, and dispatches agents.
func (f *CLIFrontend) RunLoop(floor *Floor, ctrl *Controller, agents map[string]Agent, initialPrompt string) error {
	// Start floor infrastructure
	if err := floor.Start(func(msg string) {
		f.renderSystemInfo(msg)
	}); err != nil {
		return err
	}
	defer floor.Stop()
	defer f.Close()

	f.renderHeader(floor)

	// readyForInput signals the stdin goroutine to show the prompt.
	// It gates input so we don't show "@user:" while agents are streaming.
	readyForInput := make(chan struct{}, 1)

	// Spawn stdin reader goroutine (waits for readyForInput before each prompt)
	go f.readStdinLoop(floor, readyForInput)

	// If initial prompt, post it as @user
	if initialPrompt != "" {
		f.renderStream(AgentLabel{AgentID: "@user"})
		f.renderStream(TokenStreamed{AgentID: "@user", Token: initialPrompt + "\n"})
		floor.Chat.Post(ChatMessage{From: "@user", Content: initialPrompt})
	} else {
		// No initial prompt — ready for user input immediately
		readyForInput <- struct{}{}
	}

	var cancelAgent context.CancelFunc
	oneShot := initialPrompt != ""

	// signalReady sends readyForInput if the decision means "back to user"
	signalReady := func(d Decision) {
		if d.Action == "wait" {
			select {
			case readyForInput <- struct{}{}:
			default:
			}
		}
	}

	// Main event loop — flat, no recursion
	for ev := range floor.Chat.Events() {
		switch e := ev.(type) {
		case MessagePosted:
			f.renderMessagePosted(e)

			decision := ctrl.Decide(floor.Chat, e)
			if err := f.handleDecision(floor, ctrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			if oneShot && decision.Action == "wait" {
				return nil
			}
			signalReady(decision)

		case StreamEvent:
			f.renderStream(e.Event)

		case AgentFinished:
			f.out.Print("\n") // newline after streaming
			// AgentFinished is posted after Chat.Post (MessagePosted), so Decide
			// was already called on the MessagePosted. Nothing to do here.

		case AgentPassedEvent:
			f.out.Terminal("\r\033[K")
			f.out.Terminal("%s%s[%s]:%s [PASS]\n", Bold, f.agentColor(e.AgentID), e.AgentID, Reset)

			decision := ctrl.Decide(floor.Chat, e)
			if err := f.handleDecision(floor, ctrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			if oneShot && decision.Action == "wait" {
				return nil
			}
			signalReady(decision)

		case AgentErrorEvent:
			f.out.Terminal("\r\033[K")
			f.out.AgentLabel(e.AgentID, f.agentColor(e.AgentID))
			f.out.Print("[ERROR: %v]\n", e.Err)

			decision := ctrl.Decide(floor.Chat, e)
			if err := f.handleDecision(floor, ctrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			signalReady(decision)

		case UserCommandEvent:
			decision := ctrl.Decide(floor.Chat, e)
			if decision.Action == "stop" {
				f.out.Print("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
				return nil
			}
			if decision.Action == "clear" {
				f.out.Print("%s[Conversation cleared]%s\n", Dim, Reset)
			}
			if decision.Action == "error" {
				f.renderSystemInfo(decision.Info)
			}
			signalReady(decision)
		}
	}

	return nil
}

// handleDecision acts on a Controller Decision by dispatching agents.
func (f *CLIFrontend) handleDecision(floor *Floor, ctrl *Controller, agents map[string]Agent, d Decision, cancelAgent *context.CancelFunc) error {
	switch d.Action {
	case "trigger":
		agent, ok := agents[d.AgentID]
		if !ok {
			f.renderSystemInfo(fmt.Sprintf("[ERROR: unknown agent %s]", d.AgentID))
			return nil
		}

		// Show thinking indicator
		f.out.Print("\n")
		f.out.Terminal("%s%s[%s]:%s %sthinking...%s", Bold, f.agentColor(d.AgentID), d.AgentID, Reset, Dim, Reset)

		ctx, cancel := context.WithCancel(context.Background())
		*cancelAgent = cancel

		// Run agent in goroutine
		go func() {
			defer cancel()
			agent.Run(ctx, floor)
			// Agent.Run() posts MessagePosted (or AgentPassedEvent/AgentErrorEvent) to Chat.
			// The main loop will pick it up and call Decide again.
		}()

	case "wait":
		// Nothing — user will type, or we'll exit in one-shot mode

	case "stop":
		f.out.Print("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
		return fmt.Errorf("stop")

	case "error":
		f.renderSystemInfo(d.Info)
	}

	return nil
}

// readStdinLoop reads lines from stdin and posts them to Chat.
// Waits for readyForInput before showing the prompt (so it doesn't
// appear while agents are streaming).
func (f *CLIFrontend) readStdinLoop(floor *Floor, readyForInput chan struct{}) {
	for range readyForInput {
		f.out.Print("\n")
		f.out.AgentLabel("@user", f.agentColor("@user"))

		input, err := f.reader.ReadString('\n')
		if err != nil {
			f.out.Print("%s[Interrupted]%s\n", Dim, Reset)
			floor.Chat.PostEvent(UserCommandEvent{Command: "/quit"})
			return
		}

		text := strings.TrimSpace(input)
		f.out.Log("%s\n", text)

		if text == "" {
			// Empty line — re-prompt immediately
			select {
			case readyForInput <- struct{}{}:
			default:
			}
			continue
		}

		if strings.HasPrefix(text, "/") {
			floor.Chat.PostEvent(UserCommandEvent{Command: text})
		} else {
			floor.Chat.Post(ChatMessage{From: "@user", Content: text})
		}
	}
}

// renderMessagePosted handles display of a completed message (agent only — user already displayed).
func (f *CLIFrontend) renderMessagePosted(e MessagePosted) {
	// User messages are already rendered by readStdinLoop (agent label + typed text).
	// Agent messages are rendered via streaming (AgentLabel + TokenStreamed events).
	// So MessagePosted itself doesn't need terminal rendering — the content is already visible.
}

// renderStream handles display of streaming events.
func (f *CLIFrontend) renderStream(ev Event) {
	switch e := ev.(type) {
	case AgentLabel:
		f.out.Terminal("\r\033[K") // clear "thinking..." line
		f.out.AgentLabel(e.AgentID, f.agentColor(e.AgentID))
	case TokenStreamed:
		f.out.Print("%s", e.Token)
	case ToolCallStarted:
		f.out.Print("\n%s  ▶ %s%s\n", Dim, e.Title, Reset)
	case ToolCallResult:
		if e.Output != "" {
			display := e.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			f.out.Print("%s  %s%s\n", Dim, display, Reset)
		}
	}
}

// renderSystemInfo shows a system info message.
func (f *CLIFrontend) renderSystemInfo(text string) {
	f.out.Print("%s[System]: %s%s\n", Dim, text, Reset)
}

// renderHeader prints the floor header for the new loop.
func (f *CLIFrontend) renderHeader(floor *Floor) {
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
	f.renderSystemInfo(fmt.Sprintf("%sOFC - %s%s", Bold, floor.Blueprint.Name, Reset))
	if floor.Blueprint.Description != "" {
		f.renderSystemInfo(floor.Blueprint.Description)
	}

	var agentList []string
	for _, a := range floor.Blueprint.Agents {
		agentList = append(agentList, f.agentColor(a.ID)+a.ID+Reset)
	}
	f.renderSystemInfo(fmt.Sprintf("Agents: %s", strings.Join(agentList, ", ")))
	if len(floor.Furniture) > 0 {
		var names []string
		for name := range floor.Furniture {
			names = append(names, name)
		}
		f.renderSystemInfo(fmt.Sprintf("Furniture: %s", strings.Join(names, ", ")))
	}
	f.renderSystemInfo(fmt.Sprintf("Type %s/quit%s to exit, %s/clear%s to reset", Bold, Reset, Bold, Reset))
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
}
