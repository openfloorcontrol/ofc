package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/eval"
	"github.com/spf13/cobra"
)

var (
	evalAgent    string
	evalEndpoint string
	evalModel    string
	evalAPIKey   string
)

var evalCmd = &cobra.Command{
	Use:   "eval [prompt]",
	Short: "Evaluate stdin with an LLM",
	Long: `Read text from stdin and evaluate it with an LLM using the given prompt.

The LLM configuration comes from the project's blueprint (same -f flag as run).
Use --agent to pick a specific agent's config, or defaults to the first LLM agent.

For standalone use without a blueprint, pass --endpoint and --model directly.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		evalPrompt := args[0]

		// Read all of stdin
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		if len(input) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no input on stdin")
			os.Exit(1)
		}

		// Resolve blueprint and agent
		bp, agentID, err := resolveEvalConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Run evaluation
		result, err := eval.Run(string(input), evalPrompt, agentID, bp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Output as JSON
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(result)
	},
}

// resolveEvalConfig builds a blueprint and picks an agent ID for evaluation.
// Priority: explicit --endpoint/--model flags → blueprint file.
func resolveEvalConfig() (*blueprint.Blueprint, string, error) {
	// Standalone mode: build a minimal blueprint from flags
	if evalEndpoint != "" && evalModel != "" {
		agentID := "@eval"
		bp := &blueprint.Blueprint{
			Name: "eval",
			Agents: []blueprint.Agent{
				{
					ID:          agentID,
					Type:        "llm",
					Endpoint:    evalEndpoint,
					Model:       evalModel,
					Temperature: 0.3,
					Activation:  "mention",
					ToolContext:  "full",
				},
			},
		}
		return bp, agentID, nil
	}

	// Blueprint mode: load from file
	bp, err := blueprint.Load(blueprintFile)
	if err != nil {
		if evalEndpoint != "" || evalModel != "" {
			return nil, "", fmt.Errorf("need both --endpoint and --model for standalone mode")
		}
		return nil, "", fmt.Errorf("loading blueprint: %w\n(Use --endpoint and --model for standalone mode, or ensure blueprint.yaml exists)", err)
	}

	// Find the agent
	agentID := evalAgent
	if agentID == "" {
		// Default to first LLM agent
		for _, a := range bp.Agents {
			if a.Type == "" || a.Type == "llm" {
				agentID = a.ID
				break
			}
		}
		if agentID == "" {
			return nil, "", fmt.Errorf("no LLM agent found in blueprint")
		}
	}

	return bp, agentID, nil
}

func init() {
	evalCmd.Flags().StringVar(&evalAgent, "agent", "", "Agent ID to use for LLM config (default: first LLM agent)")
	evalCmd.Flags().StringVar(&evalEndpoint, "endpoint", "", "LLM endpoint (standalone mode, skips blueprint)")
	evalCmd.Flags().StringVar(&evalModel, "model", "", "LLM model (standalone mode, skips blueprint)")
	evalCmd.Flags().StringVar(&evalAPIKey, "api-key", os.Getenv("OFC_API_KEY"), "API key (env: OFC_API_KEY)")
	// Reuse the -f flag from run for blueprint file path
	evalCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
}
