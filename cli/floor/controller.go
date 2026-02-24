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

// Controller is the pure-logic heart of the floor.
// It receives events, updates state, and returns actions.
// It has NO I/O, NO goroutines, NO channels.
type Controller struct {
	Blueprint    *blueprint.Blueprint
	CallStack    []Frame
	passedAgents map[string]bool
	DebugFunc    func(string) // injected for debug logging; no-op in tests
}

// NewController creates a controller for the given blueprint.
func NewController(bp *blueprint.Blueprint) *Controller {
	return &Controller{
		Blueprint:    bp,
		passedAgents: make(map[string]bool),
		DebugFunc:    func(string) {}, // no-op by default
	}
}

// Decide processes a ChatEvent and returns what to do next.
// It reads messages from the Chat rather than storing them internally.
func (c *Controller) Decide(chat *Chat, ev ChatEvent) Decision {
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
		// Pop frame if this agent was the callee on top of stack
		if len(c.CallStack) > 0 && c.CallStack[len(c.CallStack)-1].Callee == e.AgentID {
			c.CallStack = c.CallStack[:len(c.CallStack)-1]
		}
		c.passedAgents[e.AgentID] = true
		return c.decideNext(chat)

	case AgentErrorEvent:
		return Decision{
			Action: "error",
			Info:   fmt.Sprintf("[ERROR from %s: %v]", e.AgentID, e.Err),
		}

	case UserCommandEvent:
		switch e.Command {
		case "/quit":
			return Decision{Action: "stop"}
		case "/clear":
			chat.Clear()
			c.CallStack = nil
			c.passedAgents = make(map[string]bool)
			return Decision{Action: "clear"}
		default:
			return Decision{Action: "error", Info: fmt.Sprintf("Unknown command: %s", e.Command)}
		}

	default:
		return Decision{Action: "wait"}
	}
}

// decideNext uses the turn-taking algorithm to pick the next agent.
func (c *Controller) decideNext(chat *Chat) Decision {
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
	for _, agent := range c.Blueprint.Agents {
		if excluded[agent.ID] {
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
	for _, agent := range c.Blueprint.Agents {
		if excluded[agent.ID] {
			c.debug("should_wake(%s): skipped (passed)", agent.ID)
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

// getAgent looks up an agent by ID.
func (c *Controller) getAgent(id string) *blueprint.Agent {
	for i := range c.Blueprint.Agents {
		if c.Blueprint.Agents[i].ID == id {
			return &c.Blueprint.Agents[i]
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

// --- Helpers ---

func summarizeLines(text string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= maxLines {
		return strings.TrimSpace(text)
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

func formatToolInteractions(interactions []ToolInteraction, level string) string {
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
			resultShort := summarizeLines(ti.Output, 3)
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
