package floor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	acpclient "github.com/openfloorcontrol/ofc/acp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/llm"
	"github.com/openfloorcontrol/ofc/sandbox"
)

// ANSI color codes
const (
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Purple = "\033[35m"
)

// ToolInteraction stores one tool call and its result
type ToolInteraction struct {
	Command string
	Output  string
}

// FloorMessage is a floor-level message (distinct from llm.Message which is for the API)
type FloorMessage struct {
	FromID           string            // "@user", "@data", "@code"
	Content          string            // The text content
	ToolInteractions []ToolInteraction // Tool calls made during this turn
}

// Frame represents one level in the delegation chain.
// Caller asked Callee a question via @mention?
type Frame struct {
	Caller string // e.g. "@data"
	Callee string // e.g. "@code"
}

// Floor is a running floor instance
type Floor struct {
	Blueprint   *blueprint.Blueprint
	Messages    []FloorMessage
	CallStack   []Frame
	Debug       bool
	Sandbox     *sandbox.Sandbox
	ACPSessions map[string]*acpclient.AgentSession // agent ID → ACP session
}

// New creates a new floor
func New(bp *blueprint.Blueprint, debug bool) *Floor {
	return &Floor{
		Blueprint: bp,
		Messages:  []FloorMessage{},
		Debug:     debug,
	}
}

// Start initializes the floor (sandbox, etc.)
func (f *Floor) Start() error {
	// Check if any agent needs tools
	needsSandbox := false
	for _, agent := range f.Blueprint.Agents {
		if agent.CanUseTools {
			needsSandbox = true
			break
		}
	}

	if needsSandbox {
		// Find sandbox workstation config from blueprint
		var image, dockerfile string
		for _, ws := range f.Blueprint.Workstations {
			if ws.Type == "sandbox" {
				image = ws.Image
				dockerfile = ws.Dockerfile
				break
			}
		}
		f.Sandbox = sandbox.New("./workspace", image, dockerfile)
		fmt.Printf("%s[System]: Starting sandbox...%s\n", Dim, Reset)
		if err := f.Sandbox.Start(); err != nil {
			return fmt.Errorf("failed to start sandbox: %w", err)
		}
		fmt.Printf("%s[System]: Sandbox ready (%s)%s\n", Dim, f.Sandbox.ContainerID[:12], Reset)
	}

	// Launch ACP agent sessions
	for _, agent := range f.Blueprint.Agents {
		if agent.Type != "acp" {
			continue
		}
		if agent.Command == "" {
			return fmt.Errorf("ACP agent %s has no command configured", agent.ID)
		}

		fmt.Printf("%s[System]: Starting ACP agent %s (%s)...%s\n", Dim, agent.ID, agent.Command, Reset)

		cwd, _ := os.Getwd()
		client := acpclient.NewFloorClient(f.Sandbox, cwd, f.Debug)
		session, err := acpclient.NewAgentSession(agent.Command, agent.Args, agent.Env, client)
		if err != nil {
			return fmt.Errorf("failed to start ACP agent %s: %w", agent.ID, err)
		}

		ctx := context.Background()
		if err := session.Initialize(ctx); err != nil {
			session.Close()
			return fmt.Errorf("failed to initialize ACP agent %s: %w", agent.ID, err)
		}
		if err := session.StartSession(ctx, cwd); err != nil {
			session.Close()
			return fmt.Errorf("failed to create session for ACP agent %s: %w", agent.ID, err)
		}

		if f.ACPSessions == nil {
			f.ACPSessions = make(map[string]*acpclient.AgentSession)
		}
		f.ACPSessions[agent.ID] = session
		fmt.Printf("%s[System]: ACP agent %s ready%s\n", Dim, agent.ID, Reset)
	}

	return nil
}

// Stop cleans up the floor
func (f *Floor) Stop() {
	for id, session := range f.ACPSessions {
		f.debug(fmt.Sprintf("closing ACP session for %s", id))
		session.Close()
	}
	if f.Sandbox != nil {
		f.Sandbox.Stop()
	}
}

func (f *Floor) debug(msg string) {
	if f.Debug {
		fmt.Printf("  [debug] %s\n", msg)
	}
}

func agentColor(id string) string {
	switch id {
	case "@data":
		return Purple
	case "@code":
		return Green
	case "@user":
		return Cyan
	default:
		return Cyan
	}
}

func printAgentLabel(id string) {
	fmt.Printf("%s%s[%s]:%s ", Bold, agentColor(id), id, Reset)
}

// summarizeLines returns the first N lines of text with a count of remaining lines
func summarizeLines(text string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= maxLines {
		return strings.TrimSpace(text)
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

// formatToolInteractions formats tool interactions based on detail level
func formatToolInteractions(interactions []ToolInteraction, level string) string {
	if level == "none" || len(interactions) == 0 {
		return ""
	}

	var parts []string
	for _, ti := range interactions {
		if level == "summary" {
			// First line of command, truncated to 80 chars
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

// buildContext converts floor messages to LLM messages for a specific agent,
// applying tool_context filtering
func (f *Floor) buildContext(agent *blueprint.Agent) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: agent.Prompt},
	}

	for _, msg := range f.Messages {
		if msg.FromID == agent.ID {
			// Own messages: role = "assistant", full tool context
			if len(msg.ToolInteractions) > 0 {
				// Emit tool call/result pairs so the agent has full memory of its own tool use
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
				// Add the text response after tool interactions
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

// shouldWake determines if an agent should respond to a message
func (f *Floor) shouldWake(agent *blueprint.Agent, lastMsg *FloorMessage) bool {
	// Never respond to own messages
	if lastMsg.FromID == agent.ID {
		return false
	}
	// "always" activation agents wake up
	if agent.Activation == "always" {
		return true
	}
	return false
}

// getAgent looks up an agent by ID
func (f *Floor) getAgent(id string) *blueprint.Agent {
	for i := range f.Blueprint.Agents {
		if f.Blueprint.Agents[i].ID == id {
			return &f.Blueprint.Agents[i]
		}
	}
	return nil
}

// nextRecipient determines which agent should respond next using the call stack
func (f *Floor) nextRecipient(excluded map[string]bool) *blueprint.Agent {
	if len(f.Messages) == 0 {
		return nil
	}

	lastMsg := f.Messages[len(f.Messages)-1]

	// Extract @mentions with ?
	mentions := extractMentions(lastMsg.Content)
	f.debug(fmt.Sprintf("next_recipient: from=%s, mentions=%v, exclude=%v, stack=%d", lastMsg.FromID, mentions, excluded, len(f.CallStack)))

	// 0. If mentions @user (and not from @user), pause for user
	if lastMsg.FromID != "@user" {
		for _, m := range mentions {
			if m == "@user" {
				f.debug("→ pausing for @user")
				return nil
			}
		}
	}

	// 1. Explicit @mentions? → push frame, wake mentioned agent
	for _, agent := range f.Blueprint.Agents {
		if excluded[agent.ID] {
			continue
		}
		for _, m := range mentions {
			if m == agent.ID && m != lastMsg.FromID {
				f.CallStack = append(f.CallStack, Frame{
					Caller: lastMsg.FromID,
					Callee: agent.ID,
				})
				f.debug(fmt.Sprintf("→ mentioned: %s (pushed frame, stack=%d)", agent.ID, len(f.CallStack)))
				return &agent
			}
		}
	}

	// 2. No mentions → pop call stack (return to caller)
	if len(f.CallStack) > 0 {
		frame := f.CallStack[len(f.CallStack)-1]
		f.CallStack = f.CallStack[:len(f.CallStack)-1]
		f.debug(fmt.Sprintf("→ pop stack: caller=%s, callee=%s (stack=%d)", frame.Caller, frame.Callee, len(f.CallStack)))

		if frame.Caller == "@user" {
			f.debug("→ caller is @user, back to user")
			return nil
		}

		caller := f.getAgent(frame.Caller)
		if caller != nil && !excluded[caller.ID] {
			return caller
		}
	}

	// 3. Poll shouldWake
	for _, agent := range f.Blueprint.Agents {
		if excluded[agent.ID] {
			f.debug(fmt.Sprintf("should_wake(%s): skipped (passed)", agent.ID))
			continue
		}
		wake := f.shouldWake(&agent, &lastMsg)
		f.debug(fmt.Sprintf("should_wake(%s): %v", agent.ID, wake))
		if wake {
			return &agent
		}
	}

	// 4. Nobody → back to user
	f.debug("→ back to user")
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

func mentionsUser(content string) bool {
	for _, m := range extractMentions(content) {
		if m == "@user" {
			return true
		}
	}
	return false
}

// buildACPContext flattens floor messages into a text prompt for ACP agents.
func (f *Floor) buildACPContext(agent *blueprint.Agent) string {
	var sb strings.Builder

	// System prompt
	if agent.Prompt != "" {
		sb.WriteString("[System] ")
		sb.WriteString(agent.Prompt)
		sb.WriteString("\n\n")
	}

	// Conversation history
	for _, msg := range f.Messages {
		sb.WriteString(msg.FromID)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")

		// Include tool interactions at the configured detail level
		if len(msg.ToolInteractions) > 0 {
			level := agent.ToolContext
			if msg.FromID == agent.ID {
				level = "full" // own tool use always full
			}
			summary := formatToolInteractions(msg.ToolInteractions, level)
			if summary != "" {
				sb.WriteString(summary)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Your turn to respond.\n")
	return sb.String()
}

// getACPAgentResponse sends a prompt to an ACP agent and collects the response.
func (f *Floor) getACPAgentResponse(agent *blueprint.Agent) (*FloorMessage, error) {
	session, ok := f.ACPSessions[agent.ID]
	if !ok {
		return nil, fmt.Errorf("no ACP session for agent %s", agent.ID)
	}

	// Build context as text
	contextText := f.buildACPContext(agent)
	f.debug(fmt.Sprintf("ACP prompt for %s (%d chars)", agent.ID, len(contextText)))

	// Set up streaming callback
	client := session.Client
	client.Reset()

	client.OnToken = func(token string) {
		fmt.Print(token)
	}

	// Clear the "thinking..." line and print label
	fmt.Printf("\r\033[K")
	printAgentLabel(agent.ID)

	// Send prompt (blocks until agent finishes)
	ctx := context.Background()
	stopReason, err := session.Prompt(ctx, contextText)
	if err != nil {
		return &FloorMessage{
			FromID:  agent.ID,
			Content: client.ResponseText.String(),
		}, fmt.Errorf("ACP prompt failed: %w", err)
	}

	fmt.Println() // newline after streaming

	f.debug(fmt.Sprintf("ACP response done: stopReason=%s, interactions=%d", stopReason, len(client.Interactions)))

	// Convert ACP tool interactions to floor tool interactions
	var interactions []ToolInteraction
	for _, ti := range client.Interactions {
		interactions = append(interactions, ToolInteraction{
			Command: ti.Command,
			Output:  ti.Output,
		})
	}

	return &FloorMessage{
		FromID:           agent.ID,
		Content:          client.ResponseText.String(),
		ToolInteractions: interactions,
	}, nil
}

// getAgentResponse calls the LLM for an agent, handling tool calls
func (f *Floor) getAgentResponse(agent *blueprint.Agent) (*FloorMessage, error) {
	client := llm.NewClient(agent.Endpoint, "")

	// Build messages with tool_context filtering
	messages := f.buildContext(agent)

	// Determine tools
	var tools []llm.Tool
	if agent.CanUseTools && f.Sandbox != nil {
		tools = []llm.Tool{llm.BashTool}
	}

	var fullResponse strings.Builder
	var interactions []ToolInteraction
	maxIterations := 10

	for i := 0; i < maxIterations; i++ {
		// Clear the "thinking..." and print streaming response
		fmt.Printf("\r\033[K") // Clear line
		printAgentLabel(agent.ID)

		result, err := client.ChatStream(agent.Model, messages, agent.Temperature, tools, func(token string) {
			fmt.Print(token)
		})
		if err != nil {
			return &FloorMessage{
				FromID:           agent.ID,
				Content:          fullResponse.String(),
				ToolInteractions: interactions,
			}, err
		}

		fullResponse.WriteString(result.Content)

		// No tool calls - we're done
		if len(result.ToolCalls) == 0 {
			fmt.Println() // Newline after streaming
			break
		}

		fmt.Println() // Newline after streaming

		// Execute tool calls
		for _, tc := range result.ToolCalls {
			if tc.Function.Name == "bash" {
				var args struct {
					Cmd string `json:"cmd"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					args.Cmd = tc.Function.Arguments
				}

				// Print the command
				fmt.Printf("%s$ %s%s\n", Dim, args.Cmd, Reset)

				// Execute
				output, err := f.Sandbox.Execute(args.Cmd)
				if err != nil {
					output = fmt.Sprintf("[ERROR: %v]", err)
				}

				// Print result (truncated for display)
				displayOutput := output
				if len(displayOutput) > 500 {
					displayOutput = displayOutput[:500] + "..."
				}
				fmt.Printf("%s%s%s\n", Dim, displayOutput, Reset)

				// Collect tool interaction for floor message
				interactions = append(interactions, ToolInteraction{
					Command: args.Cmd,
					Output:  output,
				})

				// Add tool call and result to messages for next LLM iteration
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

		// Show continuation
		fmt.Printf("  %s...%s\n", Dim, Reset)
	}

	return &FloorMessage{
		FromID:           agent.ID,
		Content:          fullResponse.String(),
		ToolInteractions: interactions,
	}, nil
}

// Run starts the floor
func (f *Floor) Run(initialPrompt string) error {
	bp := f.Blueprint

	// Start sandbox if needed
	if err := f.Start(); err != nil {
		return err
	}
	defer f.Stop()

	// Print header
	fmt.Printf("%s%s%s\n", Bold, strings.Repeat("=", 50), Reset)
	fmt.Printf("%sOFC - %s%s\n", Bold, bp.Name, Reset)
	if bp.Description != "" {
		fmt.Printf("%s%s%s\n", Dim, bp.Description, Reset)
	}

	// Print agents
	var agentList []string
	for _, a := range bp.Agents {
		agentList = append(agentList, agentColor(a.ID)+a.ID+Reset)
	}
	fmt.Printf("Agents: %s\n", strings.Join(agentList, ", "))
	fmt.Printf("Type %s/quit%s to exit, %s/clear%s to reset\n", Bold, Reset, Bold, Reset)
	fmt.Printf("%s%s%s\n", Bold, strings.Repeat("=", 50), Reset)

	oneShot := initialPrompt != ""
	firstIteration := true
	reader := bufio.NewReader(os.Stdin)

	for {
		var userInput string

		if firstIteration && initialPrompt != "" {
			userInput = initialPrompt
			fmt.Println()
			printAgentLabel("@user")
			fmt.Println(userInput)
			firstIteration = false
		} else {
			if oneShot {
				break
			}

			fmt.Println()
			printAgentLabel("@user")

			input, err := reader.ReadString('\n')
			if err != nil {
				fmt.Printf("%s[Interrupted]%s\n", Dim, Reset)
				break
			}
			userInput = strings.TrimSpace(input)
		}

		if userInput == "" {
			continue
		}

		if userInput == "/quit" {
			break
		}

		if userInput == "/clear" {
			f.Messages = []FloorMessage{}
			f.CallStack = nil
			fmt.Printf("%s[Conversation cleared]%s\n", Dim, Reset)
			continue
		}

		// Add user message
		f.Messages = append(f.Messages, FloorMessage{
			FromID:  "@user",
			Content: userInput,
		})

		// Reset call stack on new user input
		f.CallStack = nil

		// Agent loop
		passedAgents := make(map[string]bool)

		for {
			nextAgent := f.nextRecipient(passedAgents)
			if nextAgent == nil {
				break
			}

			fmt.Println()
			printAgentLabel(nextAgent.ID)
			fmt.Printf("%sthinking...%s", Dim, Reset)

			var response *FloorMessage
			var err error
			if nextAgent.Type == "acp" {
				response, err = f.getACPAgentResponse(nextAgent)
			} else {
				response, err = f.getAgentResponse(nextAgent)
			}
			if err != nil {
				fmt.Printf("\r\033[K") // Clear line
				printAgentLabel(nextAgent.ID)
				fmt.Printf("[ERROR: %v]\n", err)
				break
			}

			// Check for [PASS]
			if strings.Contains(strings.ToLower(response.Content), "[pass]") {
				fmt.Printf("\r\033[K") // Clear line
				printAgentLabel(nextAgent.ID)
				fmt.Println("[PASS]")
				// Pop frame if this agent was the callee on top of stack
				if len(f.CallStack) > 0 && f.CallStack[len(f.CallStack)-1].Callee == nextAgent.ID {
					f.CallStack = f.CallStack[:len(f.CallStack)-1]
				}
				passedAgents[nextAgent.ID] = true
				continue
			}

			// Add response to messages
			f.Messages = append(f.Messages, *response)

			// Clear passed agents on actual response
			passedAgents = make(map[string]bool)
		}
	}

	fmt.Printf("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
	return nil
}
