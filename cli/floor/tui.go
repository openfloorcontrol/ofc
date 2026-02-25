package floor

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	textareaHeight  = 3
	separatorHeight = 1
)

// TUIFrontend bridges the floor (background goroutine) with the
// Bubble Tea event loop (main thread) via p.Send().
type TUIFrontend struct {
	program  *tea.Program
	out      *Output // for log file only
	colorMap map[string]string
	debug    bool
}

// SetProgram sets the Bubble Tea program reference. Must be called before RunLoop().
func (t *TUIFrontend) SetProgram(p *tea.Program) {
	t.program = p
}

// Render sends a display event to Bubble Tea (used by cmd/run.go for debug).
func (t *TUIFrontend) Render(ev Event) {
	if t.program != nil {
		t.program.Send(ev)
	}
}

// LogWriter returns the log file writer for subsystems.
func (t *TUIFrontend) LogWriter() io.Writer {
	return t.out.LogWriter()
}

// Close closes the log file.
func (t *TUIFrontend) Close() {
	t.out.Close()
}

// --- Bubble Tea message types ---

// tuiDisplayMsg wraps a display event for injection into Bubble Tea.
type tuiDisplayMsg struct {
	Event Event
}

// tuiPassedMsg signals [PASS] in the TUI.
type tuiPassedMsg struct {
	AgentID string
}

// tuiErrorMsg signals an agent error in the TUI.
type tuiErrorMsg struct {
	AgentID string
	Err     error
}

// tuiClearedMsg signals conversation cleared.
type tuiClearedMsg struct{}

// tuiStoppedMsg signals the floor should stop.
type tuiStoppedMsg struct{}

// tuiSystemMsg displays a system info message.
type tuiSystemMsg struct {
	Text string
}

// --- Constructor ---

// NewTUIFrontend creates a TUI frontend and its Bubble Tea model.
func NewTUIFrontend(logPath string, debug bool, colorMap map[string]string) (*TUIFrontend, *tuiModel) {
	frontend := &TUIFrontend{
		out:      NewOutput(logPath, false),
		colorMap: colorMap,
		debug:    debug,
	}

	model := &tuiModel{
		colorMap: colorMap,
	}

	return frontend, model
}

// --- RunLoop ---

// RunLoop is the event-driven main loop for TUI.
// It runs a background goroutine that reads from the unified event channel
// (main floor + rooms), calls Controller.Decide(), dispatches agents,
// and sends display events to Bubble Tea via p.Send().
func (t *TUIFrontend) RunLoop(floor *Floor, ctrl *Controller, agents map[string]Agent, initialPrompt string) error {
	// Start floor infrastructure
	if err := floor.Start(func(msg string) {
		if t.program != nil {
			t.program.Send(tuiSystemMsg{Text: msg})
		}
	}); err != nil {
		return err
	}

	// Background goroutine: read Chat events, dispatch agents
	go func() {
		defer floor.Stop()

		// Render header
		if t.program != nil {
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%sOFC - %s%s", Bold, floor.Blueprint.Name, Reset)})
			if floor.Blueprint.Description != "" {
				t.program.Send(tuiSystemMsg{Text: floor.Blueprint.Description})
			}
			var agentList []string
			for _, a := range floor.Blueprint.Agents {
				color := Cyan
				if c, ok := t.colorMap[a.ID]; ok {
					color = c
				}
				agentList = append(agentList, color+a.ID+Reset)
			}
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("Agents: %s", strings.Join(agentList, ", "))})
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
		}

		// Post initial prompt if provided (or handle as command)
		if initialPrompt != "" {
			if t.program != nil {
				t.program.Send(tuiDisplayMsg{Event: AgentLabel{AgentID: "@user"}})
				t.program.Send(tuiDisplayMsg{Event: TokenStreamed{AgentID: "@user", Token: initialPrompt + "\n"}})
			}
			floor.Chat.PostUserInput(initialPrompt)
		}

		var cancelAgent context.CancelFunc

		// Use unified event channel — merges main floor + all room events
		unified := floor.StartUnified()

		for tagged := range unified {
			roomID := tagged.RoomID
			ev := tagged.Event

			// Resolve which Controller and Floor view to use
			eventCtrl, eventFloor := ctrl, floor
			if roomID != "" {
				room, ok := floor.Rooms[roomID]
				if !ok {
					continue // room closed, stale event
				}
				eventCtrl = room.Controller
				eventFloor = floor.ViewForRoom(room)
			}

			switch e := ev.(type) {
			case MessagePosted:
				t.logChatEvent(ev)
				decision := eventCtrl.Decide(eventFloor.Chat, e)
				t.dispatchDecision(eventFloor, agents, decision, &cancelAgent)

			case StreamEvent:
				if t.program != nil {
					t.program.Send(tuiDisplayMsg{Event: e.Event})
				}
				t.logStreamEvent(e.Event)

			case AgentFinished:
				if t.program != nil {
					t.program.Send(tuiDisplayMsg{Event: AgentDone{AgentID: e.AgentID}})
				}

			case AgentPassedEvent:
				if t.program != nil {
					t.program.Send(tuiPassedMsg{AgentID: e.AgentID})
				}
				t.out.Log("[%s]: [PASS]\n", e.AgentID)
				decision := eventCtrl.Decide(eventFloor.Chat, e)
				t.dispatchDecision(eventFloor, agents, decision, &cancelAgent)

			case AgentErrorEvent:
				if t.program != nil {
					t.program.Send(tuiErrorMsg{AgentID: e.AgentID, Err: e.Err})
				}
				t.out.Log("[ERROR from %s: %v]\n", e.AgentID, e.Err)
				decision := eventCtrl.Decide(eventFloor.Chat, e)
				t.dispatchDecision(eventFloor, agents, decision, &cancelAgent)

			case UserCommandEvent:
				decision := HandleCommand(e.Command, floor, ctrl)
				switch decision.Action {
				case "stop":
					if t.program != nil {
						t.program.Send(tuiStoppedMsg{})
					}
					return
				case "clear":
					if t.program != nil {
						t.program.Send(tuiClearedMsg{})
					}
				case "room_created", "room_closed":
					if t.program != nil {
						t.program.Send(tuiSystemMsg{Text: decision.Info})
					}
				case "error":
					if t.program != nil {
						t.program.Send(tuiSystemMsg{Text: decision.Info})
					}
				}
			}
		}
	}()

	return nil
}

func (t *TUIFrontend) dispatchDecision(floor *Floor, agents map[string]Agent, d Decision, cancelAgent *context.CancelFunc) {
	switch d.Action {
	case "trigger":
		agent, ok := agents[d.AgentID]
		if !ok {
			if t.program != nil {
				t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("[ERROR: unknown agent %s]", d.AgentID)})
			}
			return
		}

		// Show thinking
		if t.program != nil {
			t.program.Send(tuiDisplayMsg{Event: AgentThinking{AgentID: d.AgentID}})
		}

		ctx, cancel := context.WithCancel(context.Background())
		*cancelAgent = cancel

		go func() {
			defer cancel()
			agent.Run(ctx, floor)
		}()
	}
}

func (t *TUIFrontend) logChatEvent(ev ChatEvent) {
	switch e := ev.(type) {
	case MessagePosted:
		if e.Message.From != "@user" {
			t.out.Log("\n")
		}
	}
}

func (t *TUIFrontend) logStreamEvent(ev Event) {
	switch e := ev.(type) {
	case AgentLabel:
		t.out.Log("\n[%s]: ", e.AgentID)
	case TokenStreamed:
		t.out.Log("%s", e.Token)
	case ToolCallStarted:
		t.out.Log("\n  > %s\n", e.Title)
	case ToolCallResult:
		if e.Output != "" {
			t.out.Log("  %s\n", e.Output)
		}
	}
}

// --- tuiModel: Bubble Tea Model ---
// Posts directly to Chat instead of using inputCh.

type tuiModel struct {
	viewport viewport.Model
	textarea textarea.Model
	content  strings.Builder
	chat     *Chat // set before program starts
	colorMap map[string]string
	ready    bool
	width    int
	height   int
}

// SetChat sets the Chat reference for posting user messages.
func (m *tuiModel) SetChat(chat *Chat) {
	m.chat = chat
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
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.chat != nil {
				m.chat.PostEvent(UserCommandEvent{Command: "/quit"})
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

			// Display user input
			userColor := Cyan
			if c, ok := m.colorMap["@user"]; ok {
				userColor = c
			}
			m.appendContent(fmt.Sprintf("\n%s%s[@user]:%s %s\n", Bold, userColor, Reset, text))

			// Post to Chat
			if m.chat != nil {
				m.chat.PostUserInput(text)
			}
			return m, nil
		}

		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	// --- Display events from background goroutine ---

	case tuiDisplayMsg:
		return m.handleDisplayEvent(msg.Event)

	case tuiSystemMsg:
		m.appendContent(fmt.Sprintf("%s%s%s\n", Dim, msg.Text, Reset))
		return m, nil

	case tuiPassedMsg:
		color := m.agentColor(msg.AgentID)
		m.replaceThinking(msg.AgentID)
		m.appendContent(fmt.Sprintf("%s%s[%s]:%s [PASS]\n", Bold, color, msg.AgentID, Reset))
		return m, nil

	case tuiErrorMsg:
		m.appendContent(fmt.Sprintf("\n%s[ERROR from %s: %v]%s\n", Red, msg.AgentID, msg.Err, Reset))
		return m, nil

	case tuiClearedMsg:
		m.content.Reset()
		if m.ready {
			m.viewport.SetContent("")
			m.viewport.GotoTop()
		}
		m.appendContent(fmt.Sprintf("%s[Conversation cleared]%s\n", Dim, Reset))
		return m, nil

	case tuiStoppedMsg:
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) handleDisplayEvent(ev Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case AgentThinking:
		color := m.agentColor(e.AgentID)
		m.appendContent(fmt.Sprintf("\n%s%s[%s]:%s %sthinking...%s", Bold, color, e.AgentID, Reset, Dim, Reset))
	case AgentLabel:
		m.replaceThinking(e.AgentID)
	case TokenStreamed:
		m.appendContent(e.Token)
	case ToolCallStarted:
		m.appendContent(fmt.Sprintf("\n%s  > %s%s\n", Dim, e.Title, Reset))
	case ToolCallResult:
		if e.Output != "" {
			display := e.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			m.appendContent(fmt.Sprintf("%s  %s%s\n", Dim, display, Reset))
		}
	case AgentDone:
		m.appendContent("\n")
	}
	return m, nil
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

func (m *tuiModel) appendContent(text string) {
	m.content.WriteString(text)
	if m.ready {
		m.viewport.SetContent(m.content.String())
		m.viewport.GotoBottom()
	}
}

func (m *tuiModel) replaceThinking(agentID string) {
	content := m.content.String()
	thinkSuffix := fmt.Sprintf("%sthinking...%s", Dim, Reset)
	if idx := strings.LastIndex(content, thinkSuffix); idx >= 0 {
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
