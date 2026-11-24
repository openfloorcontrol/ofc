package frontend

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openfloorcontrol/ofc/floor"
)

// CLIFrontend provides terminal-based interaction for the floor.
type CLIFrontend struct {
	out      *Output
	colorMap map[string]string
	lines    lineReader

	// Reasoning is collapsed into a single line that is rewritten in place
	// and cleared once the answer starts, so it never fills the transcript.
	agentLabel    string // last rendered label, restored after clearing
	thinking      bool
	thinkingSince time.Time
}

// NewCLI creates a CLI frontend with terminal output and optional log
// file. On a terminal, input lines can be edited and are kept in
// historyFile ("" for no history).
func NewCLI(logPath string, debug bool, colorMap map[string]string, historyFile string) *CLIFrontend {
	return &CLIFrontend{
		out:      NewOutput(logPath, debug),
		colorMap: colorMap,
		lines:    newLineReader(historyFile),
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

// Close closes the input and the log file.
func (f *CLIFrontend) Close() {
	f.lines.Close()
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

// RunLoop renders the session's events and feeds stdin to it until the
// session stops (or, with an initial prompt, until the session waits for
// the user). The session may be local or remote.
func (f *CLIFrontend) RunLoop(client SessionClient, initial Prompt) error {
	defer f.Close()
	defer client.Close()

	f.renderHeader(client.Info())

	// If the session has prior history (resumed), replay the last turn so
	// the user sees the context they're picking up.
	f.renderLastTurnIfAny(client.History())

	events := client.Events()

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
	stdinDone := make(chan struct{})
	go f.readStdinLoop(client, readyForInput, stdinDone)

	// If initial prompt, post it as @user (or handle as command)
	if !initial.Empty() {
		f.renderStream(floor.AgentLabel{AgentID: "@user"}, "")
		f.renderStream(floor.TokenStreamed{AgentID: "@user", Token: initial.Text + "\n"}, "")
		for _, path := range initial.Images {
			f.out.Print("%s[📎 %s]%s\n", Dim, path, Reset)
		}
		if err := initial.post(client); err != nil {
			return err
		}
	} else {
		signalReady()
	}

	oneShot := initial.oneShot()

	for {
		var tagged floor.TaggedEvent
		select {
		case <-stdinDone:
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			tagged = ev
		}

		switch e := tagged.Event.(type) {
		case floor.StreamEvent:
			f.renderStream(e.Event, tagged.RoomID)

		case floor.AgentFinished:
			f.clearThinking()
			f.out.Print("\n") // newline after streaming

		case floor.MessagePosted:
			// An agent's streamed answer is complete: end its line, so
			// what follows starts after an empty line.
			if from := e.Message.From; from != "@user" && from != "@system" {
				f.out.Print("\n")
			}

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
				f.out.Print("\n")
				return nil
			}
			signalReady()
		}
	}
}

// readStdinLoop reads lines from stdin and posts them to the session.
// Waits for readyForInput before showing the prompt (so it doesn't
// appear while agents are streaming). Closes stdinDone at end of input:
// that ends this frontend, not the session.
//
// /attach <path> uploads an image and stages it for the next message;
// /attach alone lists the staged images, /detach drops them. These
// commands stay in the frontend: the session never sees them.
func (f *CLIFrontend) readStdinLoop(client SessionClient, readyForInput chan struct{}, stdinDone chan<- struct{}) {
	var staged []floor.Attachment
	reprompt := func() {
		select {
		case readyForInput <- struct{}{}:
		default:
		}
	}
	for range readyForInput {
		f.out.Print("\n")
		prompt := fmt.Sprintf("%s%s[%s]:%s ", Bold, f.agentColor("@user"), "@user", Reset)
		f.out.Log("%s", prompt)

		input, err := f.lines.ReadLine(prompt)
		if err != nil {
			f.out.Print("%s[Interrupted]%s\n", Dim, Reset)
			close(stdinDone)
			return
		}

		text := strings.TrimSpace(input)
		f.out.Log("%s\n", text)

		if text == "" {
			// Empty line — re-prompt immediately
			reprompt()
			continue
		}

		if cmd, arg, _ := strings.Cut(text, " "); cmd == "/attach" || cmd == "/detach" {
			staged = f.attachCommand(client, cmd, strings.TrimSpace(arg), staged)
			reprompt()
			continue
		}

		in := floor.UserInput{Text: text}
		if !floor.IsCommand(text) {
			in.Attachments = staged
		}
		if err := client.PostUserInput(in); err != nil {
			f.renderSystemInfo(fmt.Sprintf("[ERROR: %v]", err))
			reprompt()
			continue
		}
		if !floor.IsCommand(text) {
			staged = nil
		}
	}
}

// attachCommand runs /attach or /detach and returns the staged images.
func (f *CLIFrontend) attachCommand(client SessionClient, cmd, arg string, staged []floor.Attachment) []floor.Attachment {
	switch {
	case cmd == "/detach":
		f.renderSystemInfo(fmt.Sprintf("Dropped %d staged image(s)", len(staged)))
		return nil
	case arg == "":
		if len(staged) == 0 {
			f.renderSystemInfo("No images staged. /attach <path> stages one for your next message")
		}
		for _, a := range staged {
			f.renderSystemInfo(attachmentLabel(a))
		}
		return staged
	}
	att, err := attachFile(client, expandPath(arg))
	if err != nil {
		f.renderSystemInfo(fmt.Sprintf("[ERROR: %v]", err))
		return staged
	}
	f.renderSystemInfo(attachmentLabel(att) + " staged for your next message")
	return append(staged, att)
}

// expandPath undoes what a shell or a terminal's drag-and-drop does to a
// path: surrounding quotes and a leading ~/.
func expandPath(p string) string {
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, rest)
		}
	}
	return p
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
// RenderInfo prints a system line, e.g. floor startup progress.
func (f *CLIFrontend) RenderInfo(text string) { f.renderSystemInfo(text) }

func (f *CLIFrontend) renderSystemInfo(text string) {
	f.out.Print("%s[System]: %s%s\n", Dim, text, Reset)
}

// renderLastTurnIfAny replays the last conversational turn (last @user
// message and everything after) if the session has prior history. Used
// on resume so the user sees what they're picking up. Does nothing for
// fresh sessions.
func (f *CLIFrontend) renderLastTurnIfAny(history []floor.ChatMessage) {
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
		for _, a := range msg.Attachments {
			f.out.Print("%s%s%s\n", Dim, attachmentLabel(a), Reset)
		}
	}
}

// renderHeader prints the floor header for the new loop.
func (f *CLIFrontend) renderHeader(info FloorInfo) {
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
	f.renderSystemInfo(fmt.Sprintf("%sOFC - %s%s", Bold, info.Name, Reset))
	if info.Description != "" {
		f.renderSystemInfo(info.Description)
	}

	var agentList []string
	for _, id := range info.Agents {
		agentList = append(agentList, f.agentColor(id)+id+Reset)
	}
	f.renderSystemInfo(fmt.Sprintf("Agents: %s", strings.Join(agentList, ", ")))
	if len(info.Furniture) > 0 {
		f.renderSystemInfo(fmt.Sprintf("Furniture: %s", strings.Join(info.Furniture, ", ")))
	}
	f.renderSystemInfo(fmt.Sprintf("Type %s/quit%s to exit, %s/clear%s to reset, %s/attach <path>%s to add an image", Bold, Reset, Bold, Reset, Bold, Reset))
	f.renderSystemInfo(fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset))
}
