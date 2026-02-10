package floor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

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

// Floor is a running floor instance
type Floor struct {
	Blueprint *blueprint.Blueprint
	Messages  []llm.Message
	Debug     bool
	Sandbox   *sandbox.Sandbox
}

// New creates a new floor
func New(bp *blueprint.Blueprint, debug bool) *Floor {
	return &Floor{
		Blueprint: bp,
		Messages:  []llm.Message{},
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
		f.Sandbox = sandbox.New("./workspace")
		fmt.Printf("%s[System]: Starting sandbox...%s\n", Dim, Reset)
		if err := f.Sandbox.Start(); err != nil {
			return fmt.Errorf("failed to start sandbox: %w", err)
		}
		fmt.Printf("%s[System]: Sandbox ready (%s)%s\n", Dim, f.Sandbox.ContainerID[:12], Reset)
	}

	return nil
}

// Stop cleans up the floor
func (f *Floor) Stop() {
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
			f.Messages = []llm.Message{}
			fmt.Printf("%s[Conversation cleared]%s\n", Dim, Reset)
			continue
		}

		// Add user message
		f.Messages = append(f.Messages, llm.Message{
			Role:    "user",
			Content: userInput,
			Name:    "user",
		})

		// Process agents
		passedAgents := make(map[string]bool)

		for {
			nextAgent := f.nextRecipient(passedAgents)
			if nextAgent == nil {
				break
			}

			fmt.Println()
			printAgentLabel(nextAgent.ID)
			fmt.Printf("%sthinking...%s", Dim, Reset)

			response, err := f.getAgentResponse(nextAgent)
			if err != nil {
				fmt.Printf("\r\033[K") // Clear line
				printAgentLabel(nextAgent.ID)
				fmt.Printf("[ERROR: %v]\n", err)
				break
			}

			// Check for [PASS]
			if strings.Contains(strings.ToLower(response), "[pass]") {
				fmt.Printf("\r\033[K") // Clear line
				printAgentLabel(nextAgent.ID)
				fmt.Println("[PASS]")
				passedAgents[nextAgent.ID] = true
				continue
			}

			// Add response to messages
			f.Messages = append(f.Messages, llm.Message{
				Role:    "assistant",
				Content: response,
				Name:    strings.TrimPrefix(nextAgent.ID, "@"),
			})

			// Clear passed agents on actual response
			passedAgents = make(map[string]bool)
		}
	}

	fmt.Printf("\n%sGoodbye! ofc. 🎤%s\n", Dim, Reset)
	return nil
}

// nextRecipient determines which agent should respond next
func (f *Floor) nextRecipient(excluded map[string]bool) *blueprint.Agent {
	if len(f.Messages) == 0 {
		return nil
	}

	lastMsg := f.Messages[len(f.Messages)-1]

	// Check for @mentions with ?
	mentions := extractMentions(lastMsg.Content)
	f.debug(fmt.Sprintf("next_recipient: from=%s, mentions=%v, exclude=%v", lastMsg.Name, mentions, excluded))

	// If mentions @user, stop
	for _, m := range mentions {
		if m == "@user" {
			f.debug("→ pausing for @user")
			return nil
		}
	}

	// Check mentioned agents first
	for _, agent := range f.Blueprint.Agents {
		if excluded[agent.ID] {
			continue
		}
		for _, m := range mentions {
			if m == agent.ID {
				f.debug(fmt.Sprintf("→ mentioned: %s", agent.ID))
				return &agent
			}
		}
	}

	// Check "always" activation agents
	for _, agent := range f.Blueprint.Agents {
		if excluded[agent.ID] {
			f.debug(fmt.Sprintf("should_wake(%s): skipped (passed)", agent.ID))
			continue
		}

		// Don't respond to own messages
		if "@"+lastMsg.Name == agent.ID {
			continue
		}

		wake := agent.Activation == "always"
		f.debug(fmt.Sprintf("should_wake(%s): %v", agent.ID, wake))

		if wake {
			return &agent
		}
	}

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

// getAgentResponse calls the LLM for an agent, handling tool calls
func (f *Floor) getAgentResponse(agent *blueprint.Agent) (string, error) {
	client := llm.NewClient(agent.Endpoint, "")

	// Build messages with system prompt
	messages := []llm.Message{
		{Role: "system", Content: agent.Prompt},
	}

	// Add conversation history
	for _, msg := range f.Messages {
		role := "user"
		if msg.Name != "user" && "@"+msg.Name == agent.ID {
			role = "assistant"
		}
		messages = append(messages, llm.Message{
			Role:    role,
			Content: msg.Content,
			Name:    msg.Name,
		})
	}

	// Determine tools
	var tools []llm.Tool
	if agent.CanUseTools && f.Sandbox != nil {
		tools = []llm.Tool{llm.BashTool}
	}

	var fullResponse strings.Builder
	maxIterations := 10

	for i := 0; i < maxIterations; i++ {
		// Clear the "thinking..." and print streaming response
		fmt.Printf("\r\033[K") // Clear line
		printAgentLabel(agent.ID)

		result, err := client.ChatStream(agent.Model, messages, agent.Temperature, tools, func(token string) {
			fmt.Print(token)
		})
		if err != nil {
			return fullResponse.String(), err
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

				// Print result (truncated)
				displayOutput := output
				if len(displayOutput) > 500 {
					displayOutput = displayOutput[:500] + "..."
				}
				fmt.Printf("%s%s%s\n", Dim, displayOutput, Reset)

				// Add tool call and result to messages for next iteration
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

	return fullResponse.String(), nil
}
