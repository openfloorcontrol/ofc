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
	Headless bool // skip stdin reader (web-only mode)
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
// It reads from the unified event channel (main floor + rooms), renders events,
// and dispatches agents to the correct context.
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

	sess := floor.DefaultSession()

	// If the session has prior history (resumed from disk), replay the
	// last turn so the user sees the context they're picking up.
	f.renderLastTurnIfAny(sess)

	// readyForInput signals the stdin goroutine to show the prompt.
	// It gates input so we don't show "@user:" while agents are streaming.
	readyForInput := make(chan struct{}, 1)

	if !f.Headless {
		// Spawn stdin reader goroutine (waits for readyForInput before each prompt)
		go f.readStdinLoop(floor, readyForInput)
	}

	// If initial prompt, post it as @user (or handle as command)
	if initialPrompt != "" {
		f.renderStream(AgentLabel{AgentID: "@user"}, "")
		f.renderStream(TokenStreamed{AgentID: "@user", Token: initialPrompt + "\n"}, "")
		sess.MainRoom.PostUserInput(initialPrompt)
	} else if !f.Headless {
		// No initial prompt — ready for user input immediately
		readyForInput <- struct{}{}
	}

	var cancelAgent context.CancelFunc
	oneShot := initialPrompt != "" && !IsCommand(initialPrompt)

	// signalReady sends readyForInput if the decision means "back to user"
	// Only signal for main floor events (rooms are autonomous)
	signalReady := func(roomID string, d Decision) {
		if roomID == "" && d.Action == "wait" {
			select {
			case readyForInput <- struct{}{}:
			default:
			}
		}
	}

	// Use unified event channel — merges main session chat + all room events
	unified := sess.StartUnified()

	// Main event loop — flat, no recursion
	for tagged := range unified {
		roomID := tagged.RoomID
		ev := tagged.Event

		// Resolve which Controller and Session view to use
		eventCtrl, eventSess := ctrl, sess
		if roomID != "" {
			room, ok := sess.Rooms[roomID]
			if !ok {
				continue // room closed, stale event
			}
			eventCtrl = room.Controller
			eventSess = sess.ForRoom(room)
		}

		switch e := ev.(type) {
		case MessagePosted:
			f.renderMessagePosted(e)

			decision := eventCtrl.Decide(eventSess.MainRoom, e)
			if err := f.handleDecision(eventSess, eventCtrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			if oneShot && roomID == "" && decision.Action == "wait" {
				return nil
			}
			if info := TryAutoCloseRoom(roomID, decision, sess, ctrl); info != "" {
				f.renderSystemInfo(info)
			}
			signalReady(roomID, decision)

		case StreamEvent:
			f.renderStream(e.Event, roomID)

		case AgentFinished:
			f.out.Print("\n") // newline after streaming

		case AgentPassedEvent:
			f.out.Terminal("\r\033[K")
			label := e.AgentID
			if roomID != "" {
				label = roomID + "/" + e.AgentID
			}
			f.out.Terminal("%s%s[%s]:%s [PASS]\n", Bold, f.agentColor(e.AgentID), label, Reset)

			decision := eventCtrl.Decide(eventSess.MainRoom, e)
			if err := f.handleDecision(eventSess, eventCtrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			if oneShot && roomID == "" && decision.Action == "wait" {
				return nil
			}
			if info := TryAutoCloseRoom(roomID, decision, sess, ctrl); info != "" {
				f.renderSystemInfo(info)
			}
			signalReady(roomID, decision)

		case AgentErrorEvent:
			f.out.Terminal("\r\033[K")
			f.out.AgentLabel(e.AgentID, f.agentColor(e.AgentID))
			f.out.Print("[ERROR: %v]\n", e.Err)

			decision := eventCtrl.Decide(eventSess.MainRoom, e)
			if err := f.handleDecision(eventSess, eventCtrl, agents, decision, &cancelAgent); err != nil {
				return err
			}
			signalReady(roomID, decision)

		case UserCommandEvent:
			decision := HandleCommand(e.Command, sess, ctrl)
			switch decision.Action {
			case "stop":
				f.out.Print("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
				return nil
			case "clear":
				f.out.Print("%s[Conversation cleared]%s\n", Dim, Reset)
			case "room_created", "room_closed":
				f.renderSystemInfo(decision.Info)
			case "error":
				f.renderSystemInfo(decision.Info)
			}
			signalReady("", decision)
		}
	}

	return nil
}

// handleDecision acts on a Controller Decision by dispatching agents.
func (f *CLIFrontend) handleDecision(sess *Session, ctrl *Controller, agents map[string]Agent, d Decision, cancelAgent *context.CancelFunc) error {
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
			agent.Run(ctx, sess)
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

// readStdinLoop reads lines from stdin and posts them to the default session's Chat.
// Waits for readyForInput before showing the prompt (so it doesn't
// appear while agents are streaming).
func (f *CLIFrontend) readStdinLoop(floor *Floor, readyForInput chan struct{}) {
	sess := floor.DefaultSession()
	for range readyForInput {
		f.out.Print("\n")
		f.out.AgentLabel("@user", f.agentColor("@user"))

		input, err := f.reader.ReadString('\n')
		if err != nil {
			f.out.Print("%s[Interrupted]%s\n", Dim, Reset)
			sess.MainRoom.PostEvent(UserCommandEvent{Command: "/quit"})
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

		sess.MainRoom.PostUserInput(text)
	}
}

// renderMessagePosted handles display of a completed message.
// In headless mode, user messages aren't echoed by stdin, so we render them here.
func (f *CLIFrontend) renderMessagePosted(e MessagePosted) {
	if f.Headless && e.Message.From == "@user" {
		f.out.Print("\n")
		f.out.AgentLabel("@user", f.agentColor("@user"))
		f.out.Print("%s\n", e.Message.Content)
	}
}

// renderStream handles display of streaming events.
// roomID is "" for main floor, "#name" for room events.
func (f *CLIFrontend) renderStream(ev Event, roomID string) {
	switch e := ev.(type) {
	case AgentLabel:
		f.out.Terminal("\r\033[K") // clear "thinking..." line
		label := e.AgentID
		if roomID != "" {
			label = roomID + "/" + e.AgentID
		}
		f.out.Print("%s%s[%s]:%s ", Bold, f.agentColor(e.AgentID), label, Reset)
	case TokenStreamed:
		f.out.Print("%s", e.Token)
	case ToolCallStarted:
		f.out.Print("\n%s  ▶ %s%s\n", Dim, e.Title, Reset)
	case ToolCallOutput:
		if e.Output != "" {
			f.out.Print("%s  %s%s", Dim, e.Output, Reset)
		}
	case ToolCallResult:
		if e.Output != "" {
			display := e.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			f.out.Print("%s  %s%s\n", Dim, display, Reset)
		}
	case AgentThinking:
		f.out.Terminal("%s  thinking...%s", Dim, Reset)
	}
}

// renderSystemInfo shows a system info message.
func (f *CLIFrontend) renderSystemInfo(text string) {
	f.out.Print("%s[System]: %s%s\n", Dim, text, Reset)
}

// renderLastTurnIfAny replays the last conversational turn (last @user
// message and everything after) if the session has prior history. Used
// on resume so the user sees what they're picking up. Does nothing for
// fresh sessions.
func (f *CLIFrontend) renderLastTurnIfAny(sess *Session) {
	history := sess.MainRoom.History()
	if len(history) == 0 {
		return
	}
	// Walk back to find the most recent @user message (inclusive).
	start := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].From == "@user" {
			start = i
			break
		}
	}
	turn := history[start:]

	f.renderSystemInfo(fmt.Sprintf("--- Resuming session (%d prior messages, last turn below) ---", len(history)))
	for _, msg := range turn {
		f.out.Print("\n")
		f.out.AgentLabel(msg.From, f.agentColor(msg.From))
		f.out.Print("%s\n", msg.Content)
	}
	f.renderSystemInfo("--- End of prior conversation ---")
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
