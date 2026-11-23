package floor

import (
	"context"
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
// A decision takes three steps: Observe updates the turn state for an
// event, an Addresser decides whom the room's last message addresses
// (given Request), and Next turns that address into who speaks next,
// keeping the call stack. Decide runs all three with a MentionAddresser.
//
// The Controller reads the agent set through its registry — Floor satisfies
// AgentRegistry, but tests can pass any implementation. Room-scoped
// controllers additionally filter by AllowedIDs.
type Controller struct {
	registry   AgentRegistry
	AllowedIDs map[string]bool // nil = all agents; non-nil = subset (room scope)
	CallStack  []Frame
	seq        uint64 // the latest decision Observe made necessary
	// MaxAgentTurns is how many agent turns may follow one user message
	// before the floor goes back to the user; 0 means no limit.
	MaxAgentTurns int
	// DefaultAgent gets the floor when a user message addresses nobody.
	DefaultAgent string
	// FollowupAgent gets the floor when another agent's message hands it
	// back to the user.
	FollowupAgent string
	agentTurns    int // agent turns since the last user message
	passedAgents  map[string]bool
	RoomBound     map[string]bool // agents currently in rooms — skip on main floor
	DebugFunc     func(string)    // injected for debug logging; no-op in tests
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
	if !c.Observe(ev) {
		return Decision{Action: "wait"}
	}
	addr := MentionAddresser{}.Address(context.Background(), c.Request(chat))
	return c.Next(chat, addr)
}

// Observe updates the turn state for an event that ends or starts a turn
// — a posted message, a pass or an error — and reports whether a
// decision on who speaks next is needed.
//
// A user message clears the call stack; any posted message clears the
// passes. An agent that passed or errored is retired: its frame is popped
// and it can't get the floor until the next message, so a failing agent
// can't be woken in a loop.
func (c *Controller) Observe(ev ChatEvent) bool {
	switch e := ev.(type) {
	case MessagePosted:
		if e.Message.From == "@user" {
			c.CallStack = nil
			c.agentTurns = 0
		}
		c.passedAgents = make(map[string]bool)
	case AgentPassedEvent:
		c.retire(e.AgentID)
	case AgentErrorEvent:
		c.retire(e.AgentID)
	default:
		return false
	}
	c.seq++
	return true
}

func (c *Controller) retire(agentID string) {
	if len(c.CallStack) > 0 && c.CallStack[len(c.CallStack)-1].Callee == agentID {
		c.CallStack = c.CallStack[:len(c.CallStack)-1]
	}
	c.passedAgents[agentID] = true
}

// Request describes the decision the last Observe made necessary: the
// room's messages, the agents that may get the floor (not passed, not in
// another room, activation other than "never"), the call stack and the
// default agent.
func (c *Controller) Request(chat *Room) AddressRequest {
	var agents []blueprint.Agent
	for _, a := range c.agents() {
		if c.passedAgents[a.ID] || c.RoomBound[a.ID] || a.Activation == "never" {
			continue
		}
		agents = append(agents, a)
	}
	return AddressRequest{
		Seq:          c.seq,
		History:      chat.History(),
		Agents:       agents,
		CallStack:    append([]Frame(nil), c.CallStack...),
		DefaultAgent: c.DefaultAgent,
	}
}

// Current reports whether seq identifies the latest decision, so an
// address that arrives after a newer event can be dropped.
func (c *Controller) Current(seq uint64) bool { return seq == c.seq }

// Next decides who speaks next, given whom the room's last message
// addresses:
//
//  1. A call to the user hands the floor to the user.
//  2. A call to an agent that may speak pushes a frame (caller → agent)
//     and gives it the floor.
//  3. Otherwise, if someone is waiting for an answer, the call stack is
//     popped and the floor returns to the caller.
//  4. Otherwise a chiming agent that may speak gets the floor.
//  5. Otherwise, after a user message, the DefaultAgent gets the floor.
//  6. Otherwise the floor goes to the user — after another agent's
//     message, the FollowupAgent gets it first.
//
// Once MaxAgentTurns agents have had the floor since the last user
// message, it goes to the user instead, with an Info saying so.
func (c *Controller) Next(chat *Room, addr Address) Decision {
	messages := chat.History()
	if len(messages) == 0 {
		return Decision{Action: "wait"}
	}
	return c.NextAfter(messages[len(messages)-1], addr)
}

// NextAfter is Next for a room whose last message is last.
func (c *Controller) NextAfter(last ChatMessage, addr Address) Decision {
	c.debug("address: %s %s (%s) — from=%s, passed=%v, stack=%d",
		addr.Kind, addr.AgentID, addr.Reason, last.From, c.passedAgents, len(c.CallStack))

	if addr.Kind == AddressCall {
		if addr.AgentID == "@user" {
			c.debug("→ called @user")
			return Decision{Action: "wait"}
		}
		if c.mayGetFloor(addr.AgentID, last.From) {
			c.CallStack = append(c.CallStack, Frame{Caller: last.From, Callee: addr.AgentID})
			c.debug("→ called: %s (pushed frame, stack=%d)", addr.AgentID, len(c.CallStack))
			return c.trigger(addr.AgentID)
		}
		c.debug("→ %s can't take the floor", addr.AgentID)
	}

	if len(c.CallStack) > 0 {
		frame := c.CallStack[len(c.CallStack)-1]
		c.CallStack = c.CallStack[:len(c.CallStack)-1]
		c.debug("→ pop stack: caller=%s, callee=%s (stack=%d)", frame.Caller, frame.Callee, len(c.CallStack))
		if frame.Caller == "@user" {
			return c.backToUser(last)
		}
		if caller := c.getAgent(frame.Caller); caller != nil && !c.passedAgents[caller.ID] {
			return c.trigger(caller.ID)
		}
	}

	if addr.Kind == AddressChime && c.mayGetFloor(addr.AgentID, last.From) {
		c.debug("→ chimes in: %s", addr.AgentID)
		return c.trigger(addr.AgentID)
	}

	if last.From == "@user" && c.DefaultAgent != "" && c.mayGetFloor(c.DefaultAgent, last.From) {
		c.debug("→ default agent: %s", c.DefaultAgent)
		return c.trigger(c.DefaultAgent)
	}

	return c.backToUser(last)
}

// backToUser hands the floor to the user, unless last is an agent's
// message and the FollowupAgent, who didn't write it, may add to it first.
func (c *Controller) backToUser(last ChatMessage) Decision {
	if last.From != "@user" && c.FollowupAgent != "" && c.mayGetFloor(c.FollowupAgent, last.From) {
		c.debug("→ follow-up agent: %s", c.FollowupAgent)
		return c.trigger(c.FollowupAgent)
	}
	c.debug("→ back to user")
	return Decision{Action: "wait"}
}

// trigger gives agentID the floor, unless MaxAgentTurns agents have had
// it since the last user message.
func (c *Controller) trigger(agentID string) Decision {
	if c.MaxAgentTurns > 0 && c.agentTurns >= c.MaxAgentTurns {
		c.debug("→ max_agent_turns (%d) reached, back to user instead of %s", c.MaxAgentTurns, agentID)
		return Decision{Action: "wait", Info: fmt.Sprintf(
			"%d agent turns since your last message — the floor is yours (floor_control.max_agent_turns)", c.MaxAgentTurns)}
	}
	c.agentTurns++
	return Decision{Action: "trigger", AgentID: agentID}
}

// mayGetFloor reports whether agentID is an agent of this controller that
// hasn't passed, isn't in another room, isn't activation "never", and
// didn't write the last message.
func (c *Controller) mayGetFloor(agentID, lastFrom string) bool {
	if agentID == lastFrom || c.passedAgents[agentID] || c.RoomBound[agentID] {
		return false
	}
	a := c.getAgent(agentID)
	return a != nil && a.Activation != "never"
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
