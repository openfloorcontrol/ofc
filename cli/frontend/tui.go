package frontend

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/openfloorcontrol/ofc/floor"
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
func (t *TUIFrontend) Render(ev floor.Event) {
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
	Event floor.Event
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

// NewTUI creates a TUI frontend and its Bubble Tea model.
func NewTUI(logPath string, debug bool, colorMap map[string]string) (*TUIFrontend, *tuiModel) {
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
func (t *TUIFrontend) RunLoop(fl *floor.Floor, ctrl *floor.Controller, agents map[string]floor.Agent, initialPrompt string) error {
	// Start floor infrastructure
	if err := fl.Start(func(msg string) {
		if t.program != nil {
			t.program.Send(tuiSystemMsg{Text: msg})
		}
	}); err != nil {
		return err
	}

	// Background goroutine: read Chat events, dispatch agents
	go func() {
		defer fl.Stop()

		// Render header
		if t.program != nil {
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%sOFC - %s%s", Bold, fl.Blueprint.Name, Reset)})
			if fl.Blueprint.Description != "" {
				t.program.Send(tuiSystemMsg{Text: fl.Blueprint.Description})
			}
			var agentList []string
			for _, a := range fl.Blueprint.Agents {
				color := Cyan
				if c, ok := t.colorMap[a.ID]; ok {
					color = c
				}
				agentList = append(agentList, color+a.ID+Reset)
			}
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("Agents: %s", strings.Join(agentList, ", "))})
			t.program.Send(tuiSystemMsg{Text: fmt.Sprintf("%s%s%s", Bold, strings.Repeat("=", 50), Reset)})
		}

		sess := fl.DefaultSession()

		// Post initial prompt if provided (or handle as command)
		if initialPrompt != "" {
			if t.program != nil {
				t.program.Send(tuiDisplayMsg{Event: floor.AgentLabel{AgentID: "@user"}})
				t.program.Send(tuiDisplayMsg{Event: floor.TokenStreamed{AgentID: "@user", Token: initialPrompt + "\n"}})
			}
			sess.MainRoom.PostUserInput(initialPrompt)
		}

		var cancelAgent context.CancelFunc

		// Use unified event channel — merges main session chat + all room events
		unified := sess.StartUnified()

		onCloseInfo := func(info string) {
			if t.program != nil {
				t.program.Send(tuiSystemMsg{Text: info})
			}
		}

		for tagged := range unified {
			ec, ok := ResolveEventContext(sess, ctrl, tagged)
			if !ok {
				continue
			}
			ev := tagged.Event

			switch e := ev.(type) {
			case floor.MessagePosted:
				t.logChatEvent(ev)
				decision := DecideAndAutoClose(ec, e, sess, ctrl, onCloseInfo)
				t.dispatchDecision(ec.Sess, agents, decision, &cancelAgent)

			case floor.StreamEvent:
				if t.program != nil {
					t.program.Send(tuiDisplayMsg{Event: e.Event})
				}
				t.logStreamEvent(e.Event)

			case floor.AgentFinished:
				if t.program != nil {
					t.program.Send(tuiDisplayMsg{Event: floor.AgentDone{AgentID: e.AgentID}})
				}

			case floor.AgentPassedEvent:
				if t.program != nil {
					t.program.Send(tuiPassedMsg{AgentID: e.AgentID})
				}
				t.out.Log("[%s]: [PASS]\n", e.AgentID)
				decision := DecideAndAutoClose(ec, e, sess, ctrl, onCloseInfo)
				t.dispatchDecision(ec.Sess, agents, decision, &cancelAgent)

			case floor.AgentErrorEvent:
				if t.program != nil {
					t.program.Send(tuiErrorMsg{AgentID: e.AgentID, Err: e.Err})
				}
				t.out.Log("[ERROR from %s: %v]\n", e.AgentID, e.Err)
				decision := DecideAndAutoClose(ec, e, sess, ctrl, onCloseInfo)
				t.dispatchDecision(ec.Sess, agents, decision, &cancelAgent)

			case floor.UserCommandEvent:
				decision := floor.HandleCommand(e.Command, sess, ctrl)
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

func (t *TUIFrontend) dispatchDecision(sess *floor.Session, agents map[string]floor.Agent, d floor.Decision, cancelAgent *context.CancelFunc) {
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
			t.program.Send(tuiDisplayMsg{Event: floor.AgentThinking{AgentID: d.AgentID}})
		}

		ctx, cancel := context.WithCancel(context.Background())
		*cancelAgent = cancel

		turn := floor.NewAgentTurn(sess, sess.MainRoom, sess.Floor, d.AgentID)
		go func() {
			defer cancel()
			agent.Run(ctx, turn)
		}()
	}
}

func (t *TUIFrontend) logChatEvent(ev floor.ChatEvent) {
	switch e := ev.(type) {
	case floor.MessagePosted:
		if e.Message.From != "@user" {
			t.out.Log("\n")
		}
	}
}

func (t *TUIFrontend) logStreamEvent(ev floor.Event) {
	switch e := ev.(type) {
	case floor.AgentLabel:
		t.out.Log("\n[%s]: ", e.AgentID)
	case floor.TokenStreamed:
		t.out.Log("%s", e.Token)
	case floor.ToolCallStarted:
		t.out.Log("\n  > %s\n", e.Title)
	case floor.ToolCallResult:
		if e.Output != "" {
			t.out.Log("  %s\n", e.Output)
		}
	}
}

// --- tuiModel: Bubble Tea Model ---

type tuiModel struct {
	viewport viewport.Model
	textarea textarea.Model
	content  strings.Builder
	chat     *floor.Room // set before program starts
	colorMap map[string]string
	ready    bool
	width    int
	height   int
}

// SetChat sets the Chat reference for posting user messages.
func (m *tuiModel) SetChat(chat *floor.Room) {
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
				m.chat.PostEvent(floor.UserCommandEvent{Command: "/quit"})
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

func (m *tuiModel) handleDisplayEvent(ev floor.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case floor.AgentThinking:
		color := m.agentColor(e.AgentID)
		m.appendContent(fmt.Sprintf("\n%s%s[%s]:%s %sthinking...%s", Bold, color, e.AgentID, Reset, Dim, Reset))
	case floor.AgentLabel:
		m.replaceThinking(e.AgentID)
	case floor.TokenStreamed:
		m.appendContent(e.Token)
	case floor.ToolCallStarted:
		m.appendContent(fmt.Sprintf("\n%s  > %s%s\n", Dim, e.Title, Reset))
	case floor.ToolCallResult:
		if e.Output != "" {
			display := e.Output
			if len(display) > 500 {
				display = display[:500] + "..."
			}
			m.appendContent(fmt.Sprintf("%s  %s%s\n", Dim, display, Reset))
		}
	case floor.AgentDone:
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
