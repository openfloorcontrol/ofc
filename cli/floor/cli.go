package floor

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// CLIFrontend implements Frontend and StreamSink for terminal-based interaction.
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

// Render displays a floor event in the terminal.
func (f *CLIFrontend) Render(ev Event) {
	switch e := ev.(type) {
	case SystemInfo:
		f.out.Print("%s[System]: %s%s\n", Dim, e.Text, Reset)
	case AgentThinking:
		f.out.Print("\n")
		f.out.Terminal("%s%s[%s]:%s %sthinking...%s", Bold, f.agentColor(e.AgentID), e.AgentID, Reset, Dim, Reset)
	case ConversationCleared:
		f.out.Print("%s[Conversation cleared]%s\n", Dim, Reset)
	case AgentDone:
		f.out.Print("\n") // newline after streaming
	case AgentPassed:
		f.out.Terminal("\r\033[K")
		f.out.Terminal("%s%s[%s]:%s [PASS]\n", Bold, f.agentColor(e.AgentID), e.AgentID, Reset)
	case AgentError:
		f.out.Terminal("\r\033[K")
		f.out.AgentLabel(e.AgentID, f.agentColor(e.AgentID))
		f.out.Print("[ERROR: %v]\n", e.Err)
	case FloorStopped:
		f.out.Print("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
	case WaitingForUser:
		// nothing — ReadInput will show the prompt
	case PromptAgent:
		// nothing — coordinator handles dispatch
	}
}

// OnStream handles high-frequency streaming events from runners.
func (f *CLIFrontend) OnStream(ev Event) {
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

// ReadInput prompts the user and reads a line.
// Returns UserMessage or UserCommand, or error on EOF/interrupt.
func (f *CLIFrontend) ReadInput() (Event, error) {
	f.out.Print("\n")
	f.out.AgentLabel("@user", f.agentColor("@user"))

	input, err := f.reader.ReadString('\n')
	if err != nil {
		f.out.Print("%s[Interrupted]%s\n", Dim, Reset)
		return nil, err
	}

	text := strings.TrimSpace(input)
	f.out.Log("%s\n", text) // log user's typed input

	if text == "" {
		return f.ReadInput() // skip empty lines
	}

	if strings.HasPrefix(text, "/") {
		return UserCommand{Command: text}, nil
	}

	return UserMessage{Content: text}, nil
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
