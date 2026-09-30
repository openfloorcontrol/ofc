package frontend

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openfloorcontrol/ofc/floor"
)

// CLIFrontend provides terminal-based interaction for the floor.
type CLIFrontend struct {
	out      *Output
	colorMap map[string]string
	reader   *bufio.Reader

	// Reasoning is collapsed into a single line that is rewritten in place
	// and cleared once the answer starts, so it never fills the transcript.
	agentLabel    string // last rendered label, restored after clearing
	thinking      bool
	thinkingSince time.Time
}

// NewCLI creates a CLI frontend with terminal output and optional log file.
func NewCLI(logPath string, debug bool, colorMap map[string]string) *CLIFrontend {
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

// RunLoop starts the floor and its default session, then renders the
// session's events and feeds stdin to it until the session stops (or,
// with an initial prompt, until the session waits for the user).
func (f *CLIFrontend) RunLoop(fl *floor.Floor, initialPrompt string) error {
	// Start floor infrastructure
	if err := fl.Start(func(msg string) {
		f.renderSystemInfo(msg)
	}); err != nil {
		return err
	}
	defer fl.Stop()
	defer f.Close()

	f.renderHeader(fl)

	sess := fl.DefaultSession()

	// If the session has prior history (resumed from disk), replay the
	// last turn so the user sees the context they're picking up.
	f.renderLastTurnIfAny(sess)

	events := sess.Subscribe()
	defer sess.Unsubscribe(events)
	sess.Start()

	// readyForInput signals the stdin goroutine to show the prompt.
	// It gates input so we don't show "@user:" while agents are streaming.
	readyForInput := make(chan struct{}, 1)
	signalReady := func() {
		select {
		case readyForInput <- struct{}{}:
		default:
		}
	}

	// Stdin reader waits for readyForInput before each prompt.
	go f.readStdinLoop(sess, readyForInput)

	// If initial prompt, post it as @user (or handle as command)
	if initialPrompt != "" {
		f.renderStream(floor.AgentLabel{AgentID: "@user"}, "")
		f.renderStream(floor.TokenStreamed{AgentID: "@user", Token: initialPrompt + "\n"}, "")
		sess.MainRoom.PostUserInput(initialPrompt)
	} else {
		signalReady()
	}

	oneShot := initialPrompt != "" && !floor.IsCommand(initialPrompt)

	for tagged := range events {
		switch e := tagged.Event.(type) {
		case floor.StreamEvent:
			f.renderStream(e.Event, tagged.RoomID)

		case floor.AgentFinished:
			f.clearThinking()
			f.out.Print("\n") // newline after streaming

		case floor.AgentPassedEvent:
			f.out.Terminal("\r\033[K")
			label := e.AgentID
			if tagged.RoomID != "" {
				label = tagged.RoomID + "/" + e.AgentID
			}
			f.out.Terminal("%s%s[%s]:%s [PASS]\n", Bold, f.agentColor(e.AgentID), label, Reset)

		case floor.AgentErrorEvent:
			f.out.Terminal("\r\033[K")
			f.out.AgentLabel(e.AgentID, f.agentColor(e.AgentID))
			f.out.Print("[ERROR: %v]\n", e.Err)

		case floor.AgentStarted:
			f.out.Print("\n")
			f.out.Terminal("%s%s[%s]:%s %sthinking...%s", Bold, f.agentColor(e.AgentID), e.AgentID, Reset, Dim, Reset)

		case floor.InfoEvent:
			f.renderSystemInfo(e.Text)

		case floor.SessionCleared:
			f.out.Print("%s[Conversation cleared]%s\n", Dim, Reset)

		case floor.SessionStopped:
			f.out.Print("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
			return nil

		case floor.AwaitingInput:
			if oneShot {
				return nil
			}
			signalReady()
		}
	}

	return nil
}

// readStdinLoop reads lines from stdin and posts them to the default session's main room.
// Waits for readyForInput before showing the prompt (so it doesn't
// appear while agents are streaming).
func (f *CLIFrontend) readStdinLoop(sess *floor.Session, readyForInput chan struct{}) {
	for range readyForInput {
		f.out.Print("\n")
		f.out.AgentLabel("@user", f.agentColor("@user"))

		input, err := f.reader.ReadString('\n')
		if err != nil {
			f.out.Print("%s[Interrupted]%s\n", Dim, Reset)
			sess.MainRoom.PostEvent(floor.UserCommandEvent{Command: "/quit"})
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

// renderStream handles display of streaming events.
// roomID is "" for main floor, "#name" for room events.
func (f *CLIFrontend) renderStream(ev floor.Event, roomID string) {
	switch e := ev.(type) {
	case floor.AgentLabel:
		f.out.Terminal("\r\033[K") // clear the dispatch "thinking..." line
		label := e.AgentID
		if roomID != "" {
			label = roomID + "/" + e.AgentID
		}
		f.thinking = false
		f.agentLabel = fmt.Sprintf("%s%s[%s]:%s ", Bold, f.agentColor(e.AgentID), label, Reset)
		f.out.Print("%s", f.agentLabel)
	case floor.ThoughtStreamed:
		if !f.thinking {
			f.thinking = true
			f.thinkingSince = time.Now()
		}
		f.out.Terminal("\r\033[K%s%sthinking… (%ds)%s", f.agentLabel, Dim,
			int(time.Since(f.thinkingSince).Seconds()), Reset)
	case floor.TokenStreamed:
		f.clearThinking()
		f.out.Print("%s", e.Token)
	case floor.ToolCallStarted:
		f.clearThinking()
		f.out.Print("\n%s  ▶ %s%s\n", Dim, e.Title, Reset)
	case floor.ToolCallOutput:
		if e.Output != "" {
			f.clearThinking()
			f.out.Print("%s  %s%s", Dim, e.Output, Reset)
		}
	case floor.ToolCallResult:
		if e.Output != "" {
			f.clearThinking()
			display := e.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			f.out.Print("%s  %s%s\n", Dim, display, Reset)
		}
	}
}

// clearThinking removes the collapsed reasoning line and puts the agent label
// back. The label goes through Terminal rather than Print because AgentLabel
// already wrote it to the log file.
func (f *CLIFrontend) clearThinking() {
	if !f.thinking {
		return
	}
	f.thinking = false
	f.out.Terminal("\r\033[K%s", f.agentLabel)
}

// renderSystemInfo shows a system info message.
func (f *CLIFrontend) renderSystemInfo(text string) {
	f.out.Print("%s[System]: %s%s\n", Dim, text, Reset)
}

// renderLastTurnIfAny replays the last conversational turn (last @user
// message and everything after) if the session has prior history. Used
// on resume so the user sees what they're picking up. Does nothing for
// fresh sessions.
func (f *CLIFrontend) renderLastTurnIfAny(sess *floor.Session) {
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
}

// renderHeader prints the floor header for the new loop.
func (f *CLIFrontend) renderHeader(fl *floor.Floor) {
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
	f.renderSystemInfo(fmt.Sprintf("%sOFC - %s%s", Bold, fl.Blueprint.Name, Reset))
	if fl.Blueprint.Description != "" {
		f.renderSystemInfo(fl.Blueprint.Description)
	}

	var agentList []string
	for _, a := range fl.Blueprint.Agents {
		agentList = append(agentList, f.agentColor(a.ID)+a.ID+Reset)
	}
	f.renderSystemInfo(fmt.Sprintf("Agents: %s", strings.Join(agentList, ", ")))
	if len(fl.Furniture) > 0 {
		var names []string
		for name := range fl.Furniture {
			names = append(names, name)
		}
		f.renderSystemInfo(fmt.Sprintf("Furniture: %s", strings.Join(names, ", ")))
	}
	f.renderSystemInfo(fmt.Sprintf("Type %s/quit%s to exit, %s/clear%s to reset", Bold, Reset, Bold, Reset))
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
}
