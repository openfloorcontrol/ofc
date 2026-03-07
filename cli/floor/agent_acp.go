package floor

import (
	"context"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/openfloorcontrol/ofc/blueprint"
)

// ACPAgent is an agent backed by the Agent Client Protocol (external process).
type ACPAgent struct {
	agent *blueprint.Agent
}

// NewACPAgent creates an ACP agent from a blueprint agent definition.
func NewACPAgent(agent *blueprint.Agent) *ACPAgent {
	return &ACPAgent{agent: agent}
}

func (a *ACPAgent) AgentID() string { return a.agent.ID }

// Run executes one ACP agent turn: build context, prompt session, post results.
// Blocks until complete.
func (a *ACPAgent) Run(ctx context.Context, floor *Floor) error {
	session, ok := floor.ACPSessions[a.agent.ID]
	if !ok {
		floor.Chat.PostEvent(AgentErrorEvent{
			AgentID: a.agent.ID,
			Err:     fmt.Errorf("no ACP session for agent %s", a.agent.ID),
		})
		return fmt.Errorf("no ACP session for agent %s", a.agent.ID)
	}

	client := session.Client
	client.Reset()

	// Wire ACP callbacks to stream events
	client.OnToken = func(token string) {
		floor.Chat.PostStream(TokenStreamed{AgentID: a.agent.ID, Token: token})
	}
	client.OnToolCall = func(id, title string) {
		floor.Chat.PostStream(ToolCallStarted{AgentID: a.agent.ID, ID: id, Title: title})
	}
	client.OnToolOutput = func(id, output string) {
		floor.Chat.PostStream(ToolCallOutput{AgentID: a.agent.ID, ID: id, Output: output})
	}
	client.OnToolResult = func(id, title, output string) {
		floor.Chat.PostStream(ToolCallResult{AgentID: a.agent.ID, ID: id, Title: title, Output: output})
	}

	// Emit agent label before first token
	floor.Chat.PostStream(AgentLabel{AgentID: a.agent.ID})

	blocks := a.buildACPContext(floor)
	floor.debug("ACP prompt for %s (%d blocks)", a.agent.ID, len(blocks))

	stopReason, err := session.Prompt(ctx, blocks)
	if err != nil {
		floor.Chat.PostEvent(AgentErrorEvent{
			AgentID: a.agent.ID,
			Err:     fmt.Errorf("ACP prompt failed: %w", err),
			Partial: client.ResponseText.String(),
		})
		return err
	}
	_ = stopReason

	// Convert ACP tool interactions to floor tool interactions
	var interactions []ToolInteraction
	for _, ti := range client.Interactions {
		interactions = append(interactions, ToolInteraction{
			Command: ti.Command,
			Output:  ti.Output,
		})
	}

	content := client.ResponseText.String()

	// Check for [PASS]
	if strings.Contains(strings.ToLower(content), "[pass]") {
		floor.Chat.PostEvent(AgentPassedEvent{AgentID: a.agent.ID})
		return nil
	}

	// Post the final message to chat
	floor.Chat.Post(ChatMessage{
		From:             a.agent.ID,
		Content:          content,
		ToolInteractions: interactions,
	})
	return nil
}

// buildACPContext builds content blocks for the ACP agent prompt.
// Uses AgentContext.Delta() to send only new messages since the last prompt.
// ACP agents maintain their own session state, so we skip the agent's own
// messages (it already remembers them) and only send the system prompt once.
func (a *ACPAgent) buildACPContext(floor *Floor) []acpsdk.ContentBlock {
	var blocks []acpsdk.ContentBlock

	// Use AgentContext delta if available, fall back to full history
	var chatMsgs []*ChatMessage
	var isFirstPrompt bool

	if ac := floor.GetAgentContext(a.agent.ID); ac != nil {
		chatMsgs = ac.Delta()
		isFirstPrompt = (ac.Len() == len(chatMsgs)) // all entries are new = first prompt
	} else {
		// Fallback for contexts without AgentContext
		history := floor.Chat.History()
		chatMsgs = make([]*ChatMessage, len(history))
		for i := range history {
			chatMsgs[i] = &history[i]
		}
		isFirstPrompt = true
	}

	// Only include system prompt on first prompt (ACP agent remembers it)
	if isFirstPrompt && a.agent.Prompt != "" {
		blocks = append(blocks, acpsdk.TextBlock("[System] "+a.agent.Prompt))
	}

	for _, msg := range chatMsgs {
		// Skip own messages — ACP agent already has these in session memory
		if msg.From == a.agent.ID {
			continue
		}

		var sb strings.Builder
		if msg.From == "@system" {
			sb.WriteString("[System] ")
			sb.WriteString(msg.Content)
		} else {
			sb.WriteString(msg.From)
			sb.WriteString(": ")
			sb.WriteString(msg.Content)

			if len(msg.ToolInteractions) > 0 {
				summary := formatToolInteractions(msg.ToolInteractions, a.agent.ToolContext)
				if summary != "" {
					sb.WriteString("\n")
					sb.WriteString(summary)
				}
			}
		}

		blocks = append(blocks, acpsdk.TextBlock(sb.String()))
	}

	blocks = append(blocks, acpsdk.TextBlock("Your turn to respond."))

	// Mark all entries as sent so next Delta() only returns new messages
	if ac := floor.GetAgentContext(a.agent.ID); ac != nil {
		ac.MarkSent()
	}

	return blocks
}
