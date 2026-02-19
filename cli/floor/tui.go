package floor

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	textareaHeight = 3
	separatorHeight = 1
)

// --- TUIFrontend: implements Frontend + StreamSink ---

// TUIFrontend bridges the coordinator (background goroutine) with the
// Bubble Tea event loop (main thread) via channels and p.Send().
type TUIFrontend struct {
	program  *tea.Program
	inputCh  chan Event
	out      *Output // for log file only
	colorMap map[string]string
	debug    bool
}

// NewTUIFrontend creates a TUI frontend and its Bubble Tea model.
// Call SetProgram() after creating the tea.Program.
func NewTUIFrontend(logPath string, debug bool, colorMap map[string]string) (*TUIFrontend, *tuiModel) {
	inputCh := make(chan Event, 1)

	frontend := &TUIFrontend{
		inputCh:  inputCh,
		out:      NewOutput(logPath, false), // log file only, no terminal debug
		colorMap: colorMap,
		debug:    debug,
	}

	model := &tuiModel{
		inputCh:  inputCh,
		colorMap: colorMap,
	}

	return frontend, model
}

// SetProgram sets the Bubble Tea program reference. Must be called before Run().
func (t *TUIFrontend) SetProgram(p *tea.Program) {
	t.program = p
}

// Render sends an event to the Bubble Tea UI and logs it.
func (t *TUIFrontend) Render(ev Event) {
	if t.program != nil {
		t.program.Send(ev)
	}
	t.logEvent(ev)
}

// OnStream sends a streaming event to the Bubble Tea UI and logs it.
func (t *TUIFrontend) OnStream(ev Event) {
	if t.program != nil {
		t.program.Send(ev)
	}
	t.logEvent(ev)
}

// ReadInput blocks until the user submits input from the TUI textarea.
func (t *TUIFrontend) ReadInput() (Event, error) {
	ev, ok := <-t.inputCh
	if !ok {
		return nil, io.EOF
	}
	return ev, nil
}

// LogWriter returns the log file writer for subsystems.
func (t *TUIFrontend) LogWriter() io.Writer {
	return t.out.LogWriter()
}

// Close closes the log file.
func (t *TUIFrontend) Close() {
	t.out.Close()
}

// logEvent writes event details to the log file (no terminal output).
func (t *TUIFrontend) logEvent(ev Event) {
	switch e := ev.(type) {
	case SystemInfo:
		t.out.Log("[System]: %s\n", e.Text)
	case TokenStreamed:
		t.out.Log("%s", e.Token)
	case AgentLabel:
		t.out.Log("\n[%s]: ", e.AgentID)
	case ToolCallStarted:
		t.out.Log("\n  > %s\n", e.Title)
	case ToolCallResult:
		if e.Output != "" {
			t.out.Log("  %s\n", e.Output)
		}
	case AgentDone:
		t.out.Log("\n")
	case AgentPassed:
		t.out.Log("[%s]: [PASS]\n", e.AgentID)
	case AgentError:
		t.out.Log("[ERROR from %s: %v]\n", e.AgentID, e.Err)
	}
}

// --- tuiModel: Bubble Tea Model ---

type tuiModel struct {
	viewport viewport.Model
	textarea textarea.Model
	content  strings.Builder
	inputCh  chan<- Event
	colorMap map[string]string
	ready    bool
	width    int
	height   int
}

func (m *tuiModel) Init() tea.Cmd {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Prompt = "> "
	ta.CharLimit = 0
	ta.SetHeight(textareaHeight)
	ta.ShowLineNumbers = false
	ta.Focus()

	m.textarea = ta
	return textarea.Blink
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		vpHeight := m.height - textareaHeight - separatorHeight - 1
		if vpHeight < 1 {
			vpHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.viewport.SetContent(m.content.String())
			m.viewport.MouseWheelEnabled = true
			m.textarea.SetWidth(m.width)
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
			m.textarea.SetWidth(m.width)
			m.viewport.SetContent(m.content.String())
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			// Signal quit to coordinator
			select {
			case m.inputCh <- UserCommand{Command: "/quit"}:
			default:
			}
			return m, tea.Quit

		case tea.KeyEsc:
			select {
			case m.inputCh <- UserCommand{Command: "/quit"}:
			default:
			}
			return m, tea.Quit

		case tea.KeyCtrlL:
			return m, tea.ClearScreen

		case tea.KeyEnter:
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()

			// Display user input in viewport
			userColor := Cyan
			if c, ok := m.colorMap["@user"]; ok {
				userColor = c
			}
			m.appendContent(fmt.Sprintf("\n%s%s[@user]:%s %s\n", Bold, userColor, Reset, text))

			// Send to coordinator
			if strings.HasPrefix(text, "/") {
				select {
				case m.inputCh <- UserCommand{Command: text}:
				default:
				}
			} else {
				select {
				case m.inputCh <- UserMessage{Content: text}:
				default:
				}
			}

			return m, nil
		}

		// All other keys go to textarea
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	// --- Floor events (injected via p.Send()) ---

	case SystemInfo:
		m.appendContent(fmt.Sprintf("%s%s%s\n", Dim, msg.Text, Reset))
		return m, nil

	case AgentThinking:
		color := m.agentColor(msg.AgentID)
		m.appendContent(fmt.Sprintf("\n%s%s[%s]:%s %sthinking...%s", Bold, color, msg.AgentID, Reset, Dim, Reset))
		return m, nil

	case AgentLabel:
		// Replace "thinking..." with actual agent label
		m.replaceThinking(msg.AgentID)
		return m, nil

	case TokenStreamed:
		m.appendContent(msg.Token)
		return m, nil

	case ToolCallStarted:
		m.appendContent(fmt.Sprintf("\n%s  > %s%s\n", Dim, msg.Title, Reset))
		return m, nil

	case ToolCallResult:
		if msg.Output != "" {
			display := msg.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			m.appendContent(fmt.Sprintf("%s  %s%s\n", Dim, display, Reset))
		}
		return m, nil

	case AgentDone:
		m.appendContent("\n")
		return m, nil

	case AgentPassed:
		// Replace thinking with [PASS]
		color := m.agentColor(msg.AgentID)
		m.replaceThinking(msg.AgentID)
		m.appendContent(fmt.Sprintf("%s%s[%s]:%s [PASS]\n", Bold, color, msg.AgentID, Reset))
		return m, nil

	case AgentError:
		m.appendContent(fmt.Sprintf("\n%s[ERROR from %s: %v]%s\n", Red, msg.AgentID, msg.Err, Reset))
		return m, nil

	case ConversationCleared:
		m.content.Reset()
		if m.ready {
			m.viewport.SetContent("")
			m.viewport.GotoTop()
		}
		m.appendContent(fmt.Sprintf("%s[Conversation cleared]%s\n", Dim, Reset))
		return m, nil

	case FloorStopped:
		return m, tea.Quit

	case WaitingForUser:
		// No-op — textarea is always ready
		return m, nil

	case PromptAgent:
		// No-op — coordinator handles dispatch
		return m, nil
	}

	// Pass other messages to viewport (mouse wheel, etc.)
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render(strings.Repeat("─", m.width))

	return m.viewport.View() + "\n" + separator + "\n" + m.textarea.View()
}

// appendContent adds text to the viewport and auto-scrolls to bottom.
func (m *tuiModel) appendContent(text string) {
	m.content.WriteString(text)
	if m.ready {
		m.viewport.SetContent(m.content.String())
		m.viewport.GotoBottom()
	}
}

// replaceThinking removes the last "thinking..." line for an agent,
// replacing it with the actual agent label for streaming output.
func (m *tuiModel) replaceThinking(agentID string) {
	content := m.content.String()
	thinkSuffix := fmt.Sprintf("%sthinking...%s", Dim, Reset)
	if idx := strings.LastIndex(content, thinkSuffix); idx >= 0 {
		// Remove "thinking..." and everything after it on that line
		content = content[:idx]
		m.content.Reset()
		m.content.WriteString(content)
		if m.ready {
			m.viewport.SetContent(content)
		}
	}
}

func (m *tuiModel) agentColor(id string) string {
	if c, ok := m.colorMap[id]; ok {
		return c
	}
	return Cyan
}
