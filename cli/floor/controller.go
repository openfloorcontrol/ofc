package floor

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// Decision is the output of Controller.Decide().
type Decision struct {
	Action  string // "trigger", "wait", "stop", "clear", "error"
	AgentID string // who to trigger (if Action == "trigger")
	Info    string // additional info (error message, command name)
}

// Controller is the pure-logic heart of the floor's turn-taking.
// It receives events, updates state, and returns actions.
// It has NO I/O, NO goroutines, NO channels.
//
// The Controller reads the agent set through its registry — Floor satisfies
// AgentRegistry, but tests can pass any implementation. Room-scoped
// controllers additionally filter by AllowedIDs.
type Controller struct {
	registry     AgentRegistry
	AllowedIDs   map[string]bool // nil = all agents; non-nil = subset (room scope)
	CallStack    []Frame
	passedAgents map[string]bool
	RoomBound    map[string]bool // agents currently in rooms — skip on main floor
	DebugFunc    func(string)    // injected for debug logging; no-op in tests
}

// NewController creates a controller for the given registry's full agent set.
// In production callers pass a *Floor (which implements AgentRegistry).
func NewController(reg AgentRegistry) *Controller {
	return &Controller{
		registry:     reg,
		passedAgents: make(map[string]bool),
		RoomBound:    make(map[string]bool),
		DebugFunc:    func(string) {},
	}
}

// NewControllerForRoom creates a controller scoped to a subset of the
// registry's agents (the ones in the room). The filter is by agent ID;
// if an agent is later removed from the registry, it stops being eligible
// in the room controller as well.
func NewControllerForRoom(reg AgentRegistry, agentIDs []string) *Controller {
	filter := make(map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		filter[id] = true
	}
	return &Controller{
		registry:     reg,
		AllowedIDs:   filter,
		passedAgents: make(map[string]bool),
		RoomBound:    make(map[string]bool),
		DebugFunc:    func(string) {},
	}
}

// agents returns the agents this controller considers eligible.
// For main controllers, that's the registry's full set. For room
// controllers, only those whose IDs are in AllowedIDs.
func (c *Controller) agents() []blueprint.Agent {
	all := c.registry.Agents()
	if c.AllowedIDs == nil {
		return all
	}
	var filtered []blueprint.Agent
	for _, a := range all {
		if c.AllowedIDs[a.ID] {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// Decide processes a ChatEvent and returns what to do next.
// It reads messages from the Chat rather than storing them internally.
func (c *Controller) Decide(chat *Room, ev ChatEvent) Decision {
	switch e := ev.(type) {
	case MessagePosted:
		if e.Message.From == "@user" {
			// User message resets the turn state
			c.CallStack = nil
			c.passedAgents = make(map[string]bool)
		} else {
			// Agent finished (message posted) — reset passed agents
			c.passedAgents = make(map[string]bool)
		}
		return c.decideNext(chat)

	case AgentPassedEvent:
		return c.endTurn(chat, e.AgentID)

	case AgentErrorEvent:
		return c.endTurn(chat, e.AgentID)

	default:
		return Decision{Action: "wait"}
	}
}

// endTurn retires an agent that finished without posting a message — it
// either passed or errored. The agent's frame is popped and it is excluded
// from the next poll, so a repeatedly failing agent cannot be woken in a
// loop. The error itself reaches the user through AgentErrorEvent; as far as
// turn-taking is concerned, control simply moves on.
func (c *Controller) endTurn(chat *Room, agentID string) Decision {
	if len(c.CallStack) > 0 && c.CallStack[len(c.CallStack)-1].Callee == agentID {
		c.CallStack = c.CallStack[:len(c.CallStack)-1]
	}
	c.passedAgents[agentID] = true
	return c.decideNext(chat)
}

// decideNext uses the turn-taking algorithm to pick the next agent.
func (c *Controller) decideNext(chat *Room) Decision {
	messages := chat.History()
	if len(messages) == 0 {
		return Decision{Action: "wait"}
	}

	lastMsg := messages[len(messages)-1]
	next := c.nextRecipient(lastMsg, c.passedAgents)
	if next == nil {
		return Decision{Action: "wait"}
	}
	return Decision{Action: "trigger", AgentID: next.ID}
}

// nextRecipient is the turn-taking algorithm.
// It examines the last message for mentions, manages the call stack,
// and polls agents for activation.
func (c *Controller) nextRecipient(lastMsg ChatMessage, excluded map[string]bool) *blueprint.Agent {
	mentions := extractMentions(lastMsg.Content)
	c.debug("next_recipient: from=%s, mentions=%v, exclude=%v, stack=%d", lastMsg.From, mentions, excluded, len(c.CallStack))

	// 0. If mentions @user (and not from @user), pause for user
	if lastMsg.From != "@user" {
		for _, m := range mentions {
			if m == "@user" {
				c.debug("→ pausing for @user")
				return nil
			}
		}
	}

	// 1. Explicit @mentions? → push frame, wake mentioned agent
	agents := c.agents()
	for i := range agents {
		agent := agents[i]
		if excluded[agent.ID] || c.RoomBound[agent.ID] {
			continue
		}
		for _, m := range mentions {
			if m == agent.ID && m != lastMsg.From {
				c.CallStack = append(c.CallStack, Frame{
					Caller: lastMsg.From,
					Callee: agent.ID,
				})
				c.debug("→ mentioned: %s (pushed frame, stack=%d)", agent.ID, len(c.CallStack))
				return &agent
			}
		}
	}

	// 2. No mentions → pop call stack (return to caller)
	if len(c.CallStack) > 0 {
		frame := c.CallStack[len(c.CallStack)-1]
		c.CallStack = c.CallStack[:len(c.CallStack)-1]
		c.debug("→ pop stack: caller=%s, callee=%s (stack=%d)", frame.Caller, frame.Callee, len(c.CallStack))

		if frame.Caller == "@user" {
			c.debug("→ caller is @user, back to user")
			return nil
		}

		caller := c.getAgent(frame.Caller)
		if caller != nil && !excluded[caller.ID] {
			return caller
		}
	}

	// 3. Poll shouldWake
	for i := range agents {
		agent := agents[i]
		if excluded[agent.ID] || c.RoomBound[agent.ID] {
			c.debug("should_wake(%s): skipped (passed or in room)", agent.ID)
			continue
		}
		wake := c.shouldWake(&agent, &lastMsg)
		c.debug("should_wake(%s): %v", agent.ID, wake)
		if wake {
			return &agent
		}
	}

	c.debug("→ back to user")
	return nil
}

// shouldWake determines if an agent should respond to a message.
func (c *Controller) shouldWake(agent *blueprint.Agent, lastMsg *ChatMessage) bool {
	if lastMsg.From == agent.ID {
		return false
	}
	if agent.Activation == "always" {
		return true
	}
	return false
}

// getAgent looks up an agent by ID within the controller's scope.
func (c *Controller) getAgent(id string) *blueprint.Agent {
	agents := c.agents()
	for i := range agents {
		if agents[i].ID == id {
			return &agents[i]
		}
	}
	return nil
}

func extractMentions(content string) []string {
	re := regexp.MustCompile(`@(\w+)\?`)
	matches := re.FindAllStringSubmatch(content, -1)
	var mentions []string
	for _, m := range matches {
		mentions = append(mentions, "@"+m[1])
	}
	return mentions
}

func (c *Controller) debug(format string, args ...any) {
	if c.DebugFunc != nil {
		c.DebugFunc(fmt.Sprintf(format, args...))
	}
}

// TryAutoCloseRoom checks if a room should auto-close after a decision.
// Ad-hoc rooms auto-close when the controller returns "wait" (all agents done).
// Returns info text if the room was closed, empty string otherwise.
func TryAutoCloseRoom(roomID string, d Decision, sess *Session, ctrl *Controller) string {
	if roomID == "" || d.Action != "wait" {
		return ""
	}

	room, ok := sess.Rooms[roomID]
	if !ok || room.IsClosed() {
		return ""
	}

	// Release agents from RoomBound before closing
	for aid := range room.AgentIDs {
		delete(ctrl.RoomBound, aid)
	}

	if err := sess.CloseRoom(roomID); err != nil {
		return fmt.Sprintf("Failed to auto-close room %s: %v", roomID, err)
	}

	return fmt.Sprintf("Room %s finished, agents returned to main floor", roomID)
}

// HandleCommand parses and executes a user command against the given session.
// This is the single place where all slash commands are parsed.
// Side effects (room creation, chat clearing) happen here;
// result events are posted to Chat for frontends to render.
func HandleCommand(command string, sess *Session, ctrl *Controller) Decision {
	args := strings.Fields(command)
	if len(args) == 0 {
		return Decision{Action: "error", Info: "Empty command"}
	}

	switch args[0] {
	case "/quit":
		return Decision{Action: "stop"}

	case "/clear":
		sess.MainRoom.Clear()
		ctrl.CallStack = nil
		ctrl.passedAgents = make(map[string]bool)
		return Decision{Action: "clear"}

	case "/room":
		return handleRoomCommand(args, sess, ctrl)

	default:
		return Decision{Action: "error", Info: fmt.Sprintf("Unknown command: %s", command)}
	}
}

// handleRoomCommand handles /room subcommands.
func handleRoomCommand(args []string, sess *Session, ctrl *Controller) Decision {
	if len(args) < 2 {
		return Decision{Action: "error", Info: "Usage: /room #name @agent1 @agent2 [prompt] or /room close #name"}
	}

	// /room close #name
	if args[1] == "close" {
		if len(args) < 3 {
			return Decision{Action: "error", Info: "Usage: /room close #name"}
		}
		roomID := args[2]
		if !strings.HasPrefix(roomID, "#") {
			roomID = "#" + roomID
		}

		room, ok := sess.Rooms[roomID]
		if !ok {
			return Decision{Action: "error", Info: fmt.Sprintf("Room %s not found", roomID)}
		}

		// Capture agent IDs before closing
		for aid := range room.AgentIDs {
			delete(ctrl.RoomBound, aid)
		}

		if err := sess.CloseRoom(roomID); err != nil {
			return Decision{Action: "error", Info: fmt.Sprintf("Failed to close room: %v", err)}
		}

		return Decision{Action: "room_closed", Info: fmt.Sprintf("Room %s closed, agents returned to main floor", roomID)}
	}

	// /room #name @agent1 @agent2 [prompt text]
	roomID := args[1]
	if !strings.HasPrefix(roomID, "#") {
		roomID = "#" + roomID
	}

	var agentIDs []string
	var promptParts []string
	inPrompt := false
	for _, arg := range args[2:] {
		if !inPrompt && strings.HasPrefix(arg, "@") {
			agentIDs = append(agentIDs, arg)
		} else {
			inPrompt = true
			promptParts = append(promptParts, arg)
		}
	}
	prompt := strings.Join(promptParts, " ")

	if len(agentIDs) == 0 {
		return Decision{Action: "error", Info: "Usage: /room #name @agent1 @agent2 [prompt]"}
	}

	room, err := sess.CreateRoom(roomID, "@user", agentIDs, prompt)
	if err != nil {
		return Decision{Action: "error", Info: fmt.Sprintf("Failed to create room: %v", err)}
	}

	// Wire room controller debug + mark agents as room-bound
	room.Controller.DebugFunc = ctrl.DebugFunc
	for _, aid := range agentIDs {
		ctrl.RoomBound[aid] = true
	}

	// Post initial prompt to room if provided
	if prompt != "" {
		room.Post(ChatMessage{From: "@user", Content: prompt})
	}

	return Decision{Action: "room_created", Info: fmt.Sprintf("Created room %s with %s", roomID, strings.Join(agentIDs, ", "))}
}

// --- Helpers ---

// SummarizeLines truncates a multi-line string to maxLines lines,
// appending an ellipsis summary if cut. Exported so agent subpackages
// can share the formatter.
func SummarizeLines(text string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= maxLines {
		return strings.TrimSpace(text)
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

// FormatToolInteractions renders a peer agent's tool interactions as
// text suitable for inclusion in another agent's prompt. level controls
// detail: "none" (skip), "summary" (truncated), "full" (capped at 500
// chars per output). Used by both LLM and ACP agent impls.
func FormatToolInteractions(interactions []ToolInteraction, level string) string {
	if level == "none" || len(interactions) == 0 {
		return ""
	}

	var parts []string
	for _, ti := range interactions {
		if level == "summary" {
			cmdShort := strings.SplitN(ti.Command, "\n", 2)[0]
			if len(cmdShort) > 80 {
				cmdShort = cmdShort[:80] + "..."
			}
			resultShort := SummarizeLines(ti.Output, 3)
			parts = append(parts, fmt.Sprintf("$ %s\n%s", cmdShort, resultShort))
		} else { // "full"
			output := ti.Output
			if len(output) > 500 {
				output = output[:500] + "..."
			}
			parts = append(parts, fmt.Sprintf("$ %s\n%s", ti.Command, output))
		}
	}
	return strings.Join(parts, "\n\n")
}
