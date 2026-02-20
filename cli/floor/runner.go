package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/openfloorcontrol/ofc/llm"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// RunnerResult is what a runner returns after an agent finishes.
type RunnerResult struct {
	Event Event
}

// LLMRunner executes one LLM agent turn.
type LLMRunner struct {
	Sandbox   *sandbox.Sandbox
	Stream    StreamSink
	Furniture map[string]furniture.Furniture // accessible furniture, keyed by name
}

// Run calls the LLM for an agent, handling tool calls.
// Streams tokens and tool events via r.Stream. Blocks until complete.
func (r *LLMRunner) Run(agent *blueprint.Agent, messages []llm.Message) RunnerResult {
	client := llm.NewClient(agent.Endpoint, "")

	tools := r.buildTools(agent)

	var fullResponse strings.Builder
	var interactions []ToolInteraction
	maxIterations := 10

	// Emit agent label before first token
	r.Stream.OnStream(AgentLabel{AgentID: agent.ID})

	for i := 0; i < maxIterations; i++ {
		result, err := client.ChatStream(agent.Model, messages, agent.Temperature, tools, func(token string) {
			r.Stream.OnStream(TokenStreamed{AgentID: agent.ID, Token: token})
		})
		if err != nil {
			return RunnerResult{Event: AgentError{
				AgentID: agent.ID,
				Err:     err,
				Partial: fullResponse.String(),
			}}
		}

		fullResponse.WriteString(result.Content)

		// No tool calls — done
		if len(result.ToolCalls) == 0 {
			break
		}

		// Execute tool calls — expand concatenated calls into separate entries
		expanded := r.expandToolCalls(agent.ID, result.ToolCalls)
		for _, ex := range expanded {
			r.Stream.OnStream(ToolCallResult{AgentID: agent.ID, Title: ex.Title, Output: ex.Output})

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

	// Check for [PASS]
	if strings.Contains(strings.ToLower(content), "[pass]") {
		return RunnerResult{Event: AgentPassed{AgentID: agent.ID}}
	}

	return RunnerResult{Event: AgentDone{
		AgentID:          agent.ID,
		Content:          content,
		ToolInteractions: interactions,
	}}
}

// buildTools constructs the tool list for an LLM agent, including bash and furniture tools.
func (r *LLMRunner) buildTools(agent *blueprint.Agent) []llm.Tool {
	var tools []llm.Tool
	if agent.CanUseTools && r.Sandbox != nil {
		tools = append(tools, llm.BashTool)
	}
	for _, fname := range agent.Furniture {
		f, ok := r.Furniture[fname]
		if !ok {
			continue
		}
		for _, t := range f.Tools() {
			tools = append(tools, furnitureToolToLLM(fname, t))
		}
	}
	return tools
}

// expandedCall holds one tool call with its result, ready for the message history.
type expandedCall struct {
	Call   llm.ToolCall
	Title  string
	Output string
}

// expandToolCalls processes tool calls, splitting concatenated JSON arguments
// into separate calls so the conversation history stays valid for the LLM API.
func (r *LLMRunner) expandToolCalls(agentID string, toolCalls []llm.ToolCall) []expandedCall {
	var result []expandedCall
	for _, tc := range toolCalls {
		result = append(result, r.dispatchToolCall(agentID, tc)...)
	}
	return result
}

// dispatchToolCall executes a tool call. Returns one or more expandedCalls
// (multiple if the provider concatenated arguments).
func (r *LLMRunner) dispatchToolCall(agentID string, tc llm.ToolCall) []expandedCall {
	name := tc.Function.Name

	// Check for furniture tool (namespaced as furniture__tool)
	if parts := strings.SplitN(name, "__", 2); len(parts) == 2 {
		furnitureName, toolName := parts[0], parts[1]
		f, ok := r.Furniture[furnitureName]
		if !ok {
			return []expandedCall{{
				Call:   tc,
				Title:  name,
				Output: fmt.Sprintf("[ERROR: unknown furniture %q]", furnitureName),
			}}
		}

		title := fmt.Sprintf("%s.%s", furnitureName, toolName)

		// Some providers concatenate multiple tool call arguments into one
		// (e.g. {"title":"a"}{"title":"b"}). Use json.Decoder to split them.
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
			r.Stream.OnStream(ToolCallStarted{AgentID: agentID, Title: title})

			callResult, err := f.Call(toolName, args)
			var output string
			if err != nil {
				output = fmt.Sprintf("[ERROR: %v]", err)
			} else {
				data, _ := json.Marshal(callResult)
				output = string(data)
			}

			// Build a clean tool call with valid single-object arguments
			argsJSON, _ := json.Marshal(args)
			call := llm.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
			}
			call.Function.Name = tc.Function.Name
			call.Function.Arguments = string(argsJSON)
			// Give each split call a unique ID so the API accepts them
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

	// Default: bash tool
	if name == "bash" {
		if r.Sandbox == nil {
			return []expandedCall{{Call: tc, Title: "bash", Output: "[ERROR: no sandbox available]"}}
		}

		var args struct {
			Cmd string `json:"cmd"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			args.Cmd = tc.Function.Arguments
		}

		r.Stream.OnStream(ToolCallStarted{AgentID: agentID, Title: args.Cmd})

		output, err := r.Sandbox.Execute(args.Cmd)
		if err != nil {
			return []expandedCall{{Call: tc, Title: args.Cmd, Output: fmt.Sprintf("[ERROR: %v]", err)}}
		}
		return []expandedCall{{Call: tc, Title: args.Cmd, Output: output}}
	}

	return []expandedCall{{Call: tc, Title: name, Output: fmt.Sprintf("[ERROR: unknown tool %q]", name)}}
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

// ACPRunner executes one ACP agent turn.
type ACPRunner struct {
	Sessions map[string]*acpclient.AgentSession
	Stream   StreamSink
}

// Run sends a prompt to an ACP agent and collects the response.
// Streams tokens and tool events via r.Stream. Blocks until complete.
func (r *ACPRunner) Run(agent *blueprint.Agent, blocks []acpsdk.ContentBlock) RunnerResult {
	session, ok := r.Sessions[agent.ID]
	if !ok {
		return RunnerResult{Event: AgentError{
			AgentID: agent.ID,
			Err:     fmt.Errorf("no ACP session for agent %s", agent.ID),
		}}
	}

	client := session.Client
	client.Reset()

	// Wire ACP callbacks to stream events
	client.OnToken = func(token string) {
		r.Stream.OnStream(TokenStreamed{AgentID: agent.ID, Token: token})
	}
	client.OnToolCall = func(title string) {
		r.Stream.OnStream(ToolCallStarted{AgentID: agent.ID, Title: title})
	}
	client.OnToolResult = func(title, output string) {
		r.Stream.OnStream(ToolCallResult{AgentID: agent.ID, Title: title, Output: output})
	}

	// Emit agent label before first token
	r.Stream.OnStream(AgentLabel{AgentID: agent.ID})

	ctx := context.Background()
	stopReason, err := session.Prompt(ctx, blocks)
	if err != nil {
		return RunnerResult{Event: AgentError{
			AgentID: agent.ID,
			Err:     fmt.Errorf("ACP prompt failed: %w", err),
			Partial: client.ResponseText.String(),
		}}
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
		return RunnerResult{Event: AgentPassed{AgentID: agent.ID}}
	}

	return RunnerResult{Event: AgentDone{
		AgentID:          agent.ID,
		Content:          content,
		ToolInteractions: interactions,
	}}
}
