package floor

import (
	"fmt"
	"regexp"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/llm"
)

// Controller is the pure-logic heart of the floor.
// It receives events, updates state, and returns actions.
// It has NO I/O, NO goroutines, NO channels.
type Controller struct {
	Blueprint    *blueprint.Blueprint
	Messages     []FloorMessage
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

// HandleEvent processes one event and returns zero or more response events.
func (c *Controller) HandleEvent(ev Event) []Event {
	switch e := ev.(type) {
	case UserMessage:
		return c.handleUserMessage(e)
	case AgentDone:
		return c.handleAgentDone(e)
	case AgentPassed:
		return c.handleAgentPassed(e)
	case AgentError:
		return c.handleAgentError(e)
	case UserCommand:
		return c.handleUserCommand(e)
	default:
		return nil
	}
}

func (c *Controller) handleUserMessage(e UserMessage) []Event {
	c.Messages = append(c.Messages, FloorMessage{
		FromID:  "@user",
		Content: e.Content,
	})
	c.CallStack = nil
	c.passedAgents = make(map[string]bool)
	return c.advanceTurn()
}

func (c *Controller) handleAgentDone(e AgentDone) []Event {
	c.Messages = append(c.Messages, FloorMessage{
		FromID:           e.AgentID,
		Content:          e.Content,
		ToolInteractions: e.ToolInteractions,
	})
	c.passedAgents = make(map[string]bool)
	return c.advanceTurn()
}

func (c *Controller) handleAgentPassed(e AgentPassed) []Event {
	// Pop frame if this agent was the callee on top of stack
	if len(c.CallStack) > 0 && c.CallStack[len(c.CallStack)-1].Callee == e.AgentID {
		c.CallStack = c.CallStack[:len(c.CallStack)-1]
	}
	c.passedAgents[e.AgentID] = true
	return c.advanceTurn()
}

func (c *Controller) handleAgentError(e AgentError) []Event {
	return []Event{
		SystemInfo{Text: fmt.Sprintf("[ERROR from %s: %v]", e.AgentID, e.Err)},
		WaitingForUser{},
	}
}

func (c *Controller) handleUserCommand(e UserCommand) []Event {
	switch e.Command {
	case "/quit":
		return []Event{FloorStopped{}}
	case "/clear":
		c.Messages = nil
		c.CallStack = nil
		c.passedAgents = make(map[string]bool)
		return []Event{ConversationCleared{}}
	default:
		return []Event{SystemInfo{Text: fmt.Sprintf("Unknown command: %s", e.Command)}}
	}
}

// advanceTurn calls nextRecipient and returns the appropriate event.
func (c *Controller) advanceTurn() []Event {
	next := c.nextRecipient(c.passedAgents)
	if next == nil {
		return []Event{WaitingForUser{}}
	}
	return []Event{PromptAgent{AgentID: next.ID}}
}

func (c *Controller) debug(format string, args ...any) {
	if c.DebugFunc != nil {
		c.DebugFunc(fmt.Sprintf(format, args...))
	}
}

// --- Turn-taking logic (moved from floor.go, unchanged) ---

// nextRecipient determines which agent should respond next using the call stack.
func (c *Controller) nextRecipient(excluded map[string]bool) *blueprint.Agent {
	if len(c.Messages) == 0 {
		return nil
	}

	lastMsg := c.Messages[len(c.Messages)-1]

	// Extract @mentions with ?
	mentions := extractMentions(lastMsg.Content)
	c.debug("next_recipient: from=%s, mentions=%v, exclude=%v, stack=%d", lastMsg.FromID, mentions, excluded, len(c.CallStack))

	// 0. If mentions @user (and not from @user), pause for user
	if lastMsg.FromID != "@user" {
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
			if m == agent.ID && m != lastMsg.FromID {
				c.CallStack = append(c.CallStack, Frame{
					Caller: lastMsg.FromID,
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

	// 4. Nobody → back to user
	c.debug("→ back to user")
	return nil
}

// shouldWake determines if an agent should respond to a message.
func (c *Controller) shouldWake(agent *blueprint.Agent, lastMsg *FloorMessage) bool {
	if lastMsg.FromID == agent.ID {
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

// --- Context building (moved from floor.go, unchanged) ---

// BuildContext converts floor messages to LLM messages for a specific agent,
// applying tool_context filtering.
func (c *Controller) BuildContext(agent *blueprint.Agent) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: agent.Prompt},
	}

	for _, msg := range c.Messages {
		if msg.FromID == agent.ID {
			// Own messages: role = "assistant", full tool context
			if len(msg.ToolInteractions) > 0 {
				for i, ti := range msg.ToolInteractions {
					callID := fmt.Sprintf("call_%d", i)
					messages = append(messages, llm.Message{
						Role:    "assistant",
						Content: msg.Content,
						ToolCalls: []llm.ToolCall{
							{
								ID:   callID,
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "bash",
									Arguments: fmt.Sprintf(`{"cmd":%q}`, ti.Command),
								},
							},
						},
					})
					messages = append(messages, llm.Message{
						Role:       "tool",
						Content:    ti.Output,
						ToolCallID: callID,
					})
				}
				if msg.Content != "" {
					messages = append(messages, llm.Message{
						Role:    "assistant",
						Content: msg.Content,
					})
				}
			} else {
				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: msg.Content,
				})
			}
		} else {
			// Other participants: role = "user", apply tool_context filtering
			content := msg.Content
			if len(msg.ToolInteractions) > 0 {
				toolSummary := formatToolInteractions(msg.ToolInteractions, agent.ToolContext)
				if toolSummary != "" {
					content += "\n\n" + toolSummary
				}
			}
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: content,
				Name:    strings.TrimPrefix(msg.FromID, "@"),
			})
		}
	}

	return messages
}

// BuildACPContext builds content blocks for an ACP agent prompt.
// Each floor message becomes a separate TextBlock for structural separation.
func (c *Controller) BuildACPContext(agent *blueprint.Agent) []acpsdk.ContentBlock {
	var blocks []acpsdk.ContentBlock

	if agent.Prompt != "" {
		blocks = append(blocks, acpsdk.TextBlock("[System] "+agent.Prompt))
	}

	for _, msg := range c.Messages {
		var sb strings.Builder
		sb.WriteString(msg.FromID)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)

		if len(msg.ToolInteractions) > 0 {
			level := agent.ToolContext
			if msg.FromID == agent.ID {
				level = "full"
			}
			summary := formatToolInteractions(msg.ToolInteractions, level)
			if summary != "" {
				sb.WriteString("\n")
				sb.WriteString(summary)
			}
		}

		blocks = append(blocks, acpsdk.TextBlock(sb.String()))
	}

	blocks = append(blocks, acpsdk.TextBlock("Your turn to respond."))
	return blocks
}

// --- Helpers (moved from floor.go) ---

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
