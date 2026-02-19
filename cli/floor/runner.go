package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/llm"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// RunnerResult is what a runner returns after an agent finishes.
type RunnerResult struct {
	Event Event
}

// LLMRunner executes one LLM agent turn.
type LLMRunner struct {
	Sandbox *sandbox.Sandbox
	Stream  StreamSink
}

// Run calls the LLM for an agent, handling tool calls.
// Streams tokens and tool events via r.Stream. Blocks until complete.
func (r *LLMRunner) Run(agent *blueprint.Agent, messages []llm.Message) RunnerResult {
	client := llm.NewClient(agent.Endpoint, "")

	var tools []llm.Tool
	if agent.CanUseTools && r.Sandbox != nil {
		tools = []llm.Tool{llm.BashTool}
	}

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

		// Execute tool calls
		for _, tc := range result.ToolCalls {
			if tc.Function.Name == "bash" {
				var args struct {
					Cmd string `json:"cmd"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					args.Cmd = tc.Function.Arguments
				}

				r.Stream.OnStream(ToolCallStarted{AgentID: agent.ID, Title: args.Cmd})

				output, err := r.Sandbox.Execute(args.Cmd)
				if err != nil {
					output = fmt.Sprintf("[ERROR: %v]", err)
				}

				r.Stream.OnStream(ToolCallResult{AgentID: agent.ID, Title: args.Cmd, Output: output})

				interactions = append(interactions, ToolInteraction{
					Command: args.Cmd,
					Output:  output,
				})

				messages = append(messages, llm.Message{
					Role:      "assistant",
					ToolCalls: []llm.ToolCall{tc},
				})
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    output,
					ToolCallID: tc.ID,
				})
			}
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
