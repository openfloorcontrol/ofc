package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/llm"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// LLMAgent is an agent backed by an OpenAI-compatible LLM API.
type LLMAgent struct {
	agent *blueprint.Agent
}

// NewLLMAgent creates an LLM agent from a blueprint agent definition.
func NewLLMAgent(agent *blueprint.Agent) *LLMAgent {
	return &LLMAgent{agent: agent}
}

func (a *LLMAgent) AgentID() string { return a.agent.ID }

// Run executes one LLM agent turn: build context, call LLM, handle tool calls,
// post results to the session's chat. Blocks until complete.
func (a *LLMAgent) Run(ctx context.Context, sess *Session) error {
	client := llm.NewClient(a.agent.Endpoint, a.agent.APIKey)
	messages := a.buildContext(sess)
	tools := a.buildTools(sess)

	var fullResponse strings.Builder
	var interactions []ToolInteraction
	maxIterations := 10

	// Emit agent label before first token
	sess.MainRoom.PostStream(AgentLabel{AgentID: a.agent.ID})

	for i := 0; i < maxIterations; i++ {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		result, err := client.ChatStream(a.agent.Model, messages, a.agent.Temperature, tools, func(token string) {
			sess.MainRoom.PostStream(TokenStreamed{AgentID: a.agent.ID, Token: token})
		})
		if err != nil {
			sess.MainRoom.PostEvent(AgentErrorEvent{
				AgentID: a.agent.ID,
				Err:     err,
				Partial: fullResponse.String(),
			})
			return err
		}

		fullResponse.WriteString(result.Content)

		// No tool calls — done
		if len(result.ToolCalls) == 0 {
			break
		}

		// Execute tool calls
		expanded := a.expandToolCalls(sess, result.ToolCalls)
		for _, ex := range expanded {
			sess.MainRoom.PostStream(ToolCallResult{AgentID: a.agent.ID, ID: ex.Call.ID, Title: ex.Title, Output: ex.Output})

			interactions = append(interactions, ToolInteraction{
				Command: ex.Title,
				Output:  ex.Output,
			})

			messages = append(messages, llm.Message{
				Role:      "assistant",
				ToolCalls: []llm.ToolCall{ex.Call},
			})
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    ex.Output,
				ToolCallID: ex.Call.ID,
			})
		}
	}

	content := fullResponse.String()

	// Check for [PASS] or empty response (LLM returned nothing)
	if strings.Contains(strings.ToLower(content), "[pass]") || (strings.TrimSpace(content) == "" && len(interactions) == 0) {
		sess.MainRoom.PostEvent(AgentPassedEvent{AgentID: a.agent.ID})
		return nil
	}

	// Post the final message to chat
	sess.MainRoom.Post(ChatMessage{
		From:             a.agent.ID,
		Content:          content,
		ToolInteractions: interactions,
	})
	return nil
}

// buildContext converts the agent's accumulated context to LLM messages,
// applying tool_context filtering. Reads from AgentContext if available,
// falls back to MainRoom.History() for test agents without a context.
func (a *LLMAgent) buildContext(sess *Session) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: a.agent.Prompt},
	}

	// Use AgentContext if available, fall back to MainRoom.History()
	var chatMsgs []*ChatMessage
	if ac := sess.GetAgentContext(a.agent.ID); ac != nil {
		chatMsgs = ac.Entries()
	} else {
		history := sess.MainRoom.History()
		chatMsgs = make([]*ChatMessage, len(history))
		for i := range history {
			chatMsgs[i] = &history[i]
		}
	}

	for _, msg := range chatMsgs {
		if msg.From == "@system" {
			// System messages (room transitions, etc.)
			messages = append(messages, llm.Message{
				Role:    "system",
				Content: msg.Content,
			})
		} else if msg.From == a.agent.ID {
			// Own messages: role = "assistant", full tool context
			if len(msg.ToolInteractions) > 0 {
				// Batch all tool calls into one assistant message
				var calls []llm.ToolCall
				for i, ti := range msg.ToolInteractions {
					callID := fmt.Sprintf("call_%d", i)
					calls = append(calls, llm.ToolCall{
						ID:   callID,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      "bash",
							Arguments: fmt.Sprintf(`{"cmd":%q}`, ti.Command),
						},
					})
				}
				messages = append(messages, llm.Message{
					Role:      "assistant",
					ToolCalls: calls,
				})
				// Tool results
				for i, ti := range msg.ToolInteractions {
					callID := fmt.Sprintf("call_%d", i)
					messages = append(messages, llm.Message{
						Role:       "tool",
						Content:    ti.Output,
						ToolCallID: callID,
					})
				}
				// Final response after tool use
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
				toolSummary := formatToolInteractions(msg.ToolInteractions, a.agent.ToolContext)
				if toolSummary != "" {
					content += "\n\n" + toolSummary
				}
			}
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: content,
				Name:    strings.TrimPrefix(msg.From, "@"),
			})
		}
	}

	return messages
}

// buildTools constructs the tool list for this agent.
func (a *LLMAgent) buildTools(sess *Session) []llm.Tool {
	var tools []llm.Tool
	if a.agent.CanUseSandbox && sess.Floor.Sandbox != nil {
		tools = append(tools, llm.BashTool)
	}
	for _, fname := range a.agent.Furniture {
		f, ok := sess.Floor.Furniture[fname]
		if !ok {
			continue
		}
		for _, t := range f.Tools() {
			tools = append(tools, furnitureToolToLLM(fname, t))
		}
	}
	return tools
}

// expandToolCalls processes tool calls, splitting concatenated JSON arguments.
func (a *LLMAgent) expandToolCalls(sess *Session, toolCalls []llm.ToolCall) []expandedCall {
	var result []expandedCall
	for _, tc := range toolCalls {
		result = append(result, a.dispatchToolCall(sess, tc)...)
	}
	return result
}

// dispatchToolCall executes a tool call.
func (a *LLMAgent) dispatchToolCall(sess *Session, tc llm.ToolCall) []expandedCall {
	name := tc.Function.Name

	// Check for furniture tool (namespaced as furniture__tool)
	if parts := strings.SplitN(name, "__", 2); len(parts) == 2 {
		return a.dispatchFurnitureCall(sess, tc, parts[0], parts[1])
	}

	// Default: bash tool
	if name == "bash" {
		return a.dispatchBashCall(sess.Floor.Sandbox, tc)
	}

	return []expandedCall{{Call: tc, Title: name, Output: fmt.Sprintf("[ERROR: unknown tool %q]", name)}}
}

func (a *LLMAgent) dispatchFurnitureCall(sess *Session, tc llm.ToolCall, furnitureName, toolName string) []expandedCall {
	f, ok := sess.Floor.Furniture[furnitureName]
	if !ok {
		return []expandedCall{{
			Call:   tc,
			Title:  furnitureName + "." + toolName,
			Output: fmt.Sprintf("[ERROR: unknown furniture %q]", furnitureName),
		}}
	}

	title := fmt.Sprintf("%s.%s", furnitureName, toolName)

	argsList, err := parseJSONObjects(tc.Function.Arguments)
	if err != nil {
		return []expandedCall{{
			Call:   tc,
			Title:  title,
			Output: fmt.Sprintf("[ERROR: invalid arguments: %v]", err),
		}}
	}

	var expanded []expandedCall
	for i, args := range argsList {
		callID := tc.ID
		if i > 0 {
			callID = fmt.Sprintf("%s_%d", tc.ID, i)
		}
		sess.MainRoom.PostStream(ToolCallStarted{AgentID: a.agent.ID, ID: callID, Title: title})

		callResult, err := f.Call(toolName, args)
		var output string
		if err != nil {
			output = fmt.Sprintf("[ERROR: %v]", err)
		} else {
			data, _ := json.Marshal(callResult)
			output = string(data)
		}

		argsJSON, _ := json.Marshal(args)
		call := llm.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
		}
		call.Function.Name = tc.Function.Name
		call.Function.Arguments = string(argsJSON)
		if i > 0 {
			call.ID = fmt.Sprintf("%s_%d", tc.ID, i)
		}

		expanded = append(expanded, expandedCall{
			Call:   call,
			Title:  title,
			Output: output,
		})
	}
	return expanded
}

func (a *LLMAgent) dispatchBashCall(sb *sandbox.Sandbox, tc llm.ToolCall) []expandedCall {
	if sb == nil {
		return []expandedCall{{Call: tc, Title: "bash", Output: "[ERROR: no sandbox available]"}}
	}

	var args struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		args.Cmd = tc.Function.Arguments
	}

	// Note: ToolCallStarted is emitted by the caller after expandToolCalls returns
	output, err := sb.Execute(args.Cmd)
	if err != nil {
		return []expandedCall{{Call: tc, Title: args.Cmd, Output: fmt.Sprintf("[ERROR: %v]", err)}}
	}
	return []expandedCall{{Call: tc, Title: args.Cmd, Output: output}}
}

// --- Shared tool helpers ---

// expandedCall holds one tool call with its result, ready for the message history.
type expandedCall struct {
	Call   llm.ToolCall
	Title  string
	Output string
}

// furnitureToolToLLM converts a furniture tool to an LLM tool definition.
// Tool names are namespaced as {furniture}__{tool} to avoid collisions.
func furnitureToolToLLM(furnitureName string, t furniture.Tool) llm.Tool {
	tool := llm.Tool{Type: "function"}
	tool.Function.Name = furnitureName + "__" + t.Name
	tool.Function.Description = fmt.Sprintf("[%s] %s", furnitureName, t.Description)
	tool.Function.Parameters = t.Parameters
	return tool
}

// parseJSONObjects decodes one or more concatenated JSON objects from a string.
// Handles the case where LLM providers concatenate multiple tool call arguments
// into a single string (e.g. {"a":1}{"b":2}).
func parseJSONObjects(s string) ([]map[string]interface{}, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var results []map[string]interface{}
	for dec.More() {
		var obj map[string]interface{}
		if err := dec.Decode(&obj); err != nil {
			return nil, err
		}
		results = append(results, obj)
	}
	if len(results) == 0 {
		return []map[string]interface{}{{}}, nil
	}
	return results, nil
}
