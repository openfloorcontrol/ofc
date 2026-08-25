package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/llm"
	"github.com/spf13/cobra"
)

var (
	checkFile     string
	checkEndpoint string
	checkKey      string
	checkModel    string
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check LLM endpoint / agent configuration",
	Long: `Diagnostic commands for OpenAI-compatible endpoints.

Targets are either flag-driven (--endpoint / --key / --model) or an
@agent from the current blueprint (loaded from -f, default blueprint.yaml).
With no target, ping/models iterate every agent in the blueprint.`,
}

var checkPingCmd = &cobra.Command{
	Use:   "ping [@agent]",
	Short: "Check connectivity and auth by calling /models",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targets, err := resolveTargets(argOrEmpty(args))
		if err != nil {
			fatal(err)
		}
		anyFail := false
		for _, t := range targets {
			models, err := llm.NewClient(t.Endpoint, t.APIKey).ListModels()
			if err != nil {
				anyFail = true
				fmt.Printf("%s\n  endpoint: %s\n  FAIL: %v\n", t.Label, t.Endpoint, err)
				continue
			}
			modelNote := ""
			if t.Model != "" {
				if hasModel(models, t.Model) {
					modelNote = fmt.Sprintf("  model %q: present\n", t.Model)
				} else {
					anyFail = true
					modelNote = fmt.Sprintf("  model %q: NOT in /models listing (%d available)\n", t.Model, len(models))
				}
			}
			fmt.Printf("%s\n  endpoint: %s\n  OK (%d models)\n%s", t.Label, t.Endpoint, len(models), modelNote)
		}
		if anyFail {
			os.Exit(1)
		}
	},
}

var checkModelsCmd = &cobra.Command{
	Use:   "models [@agent]",
	Short: "List models advertised by the endpoint",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targets, err := resolveTargets(argOrEmpty(args))
		if err != nil {
			fatal(err)
		}
		anyFail := false
		for i, t := range targets {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s (%s)\n", t.Label, t.Endpoint)
			models, err := llm.NewClient(t.Endpoint, t.APIKey).ListModels()
			if err != nil {
				anyFail = true
				fmt.Printf("  FAIL: %v\n", err)
				continue
			}
			ids := make([]string, len(models))
			for i, m := range models {
				ids[i] = m.ID
			}
			sort.Strings(ids)
			for _, id := range ids {
				marker := "  "
				if id == t.Model {
					marker = "* "
				}
				fmt.Printf("%s%s\n", marker, id)
			}
		}
		if anyFail {
			os.Exit(1)
		}
	},
}

var checkAskCmd = &cobra.Command{
	Use:   "ask [@agent] <prompt>",
	Short: "Send a single prompt and print the response",
	Long: `Send one chat completion and print the response.

With @agent as the first arg, the agent's rendered system prompt is
included so you're testing the agent, not just the endpoint. With flags
only, no system prompt is sent.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		agentRef, prompt := splitAskArgs(args)
		if prompt == "" {
			fatal(fmt.Errorf("no prompt given"))
		}
		targets, err := resolveTargets(agentRef)
		if err != nil {
			fatal(err)
		}
		if len(targets) != 1 {
			fatal(fmt.Errorf("ask needs a single target: pass @agent or --endpoint/--model"))
		}
		t := targets[0]
		if t.Model == "" {
			fatal(fmt.Errorf("no model resolved (pass --model or set defaults.model in the blueprint)"))
		}

		var messages []llm.Message
		if t.SystemPrompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: t.SystemPrompt})
		}
		messages = append(messages, llm.Message{Role: "user", Content: prompt})

		client := llm.NewClient(t.Endpoint, t.APIKey)
		_, err = client.ChatStream(t.Model, messages, t.Temperature, nil, llm.StreamHandler{
			OnToken: func(s string) { fmt.Print(s) },
		})
		fmt.Println()
		if err != nil {
			fatal(err)
		}
	},
}

// target is a resolved LLM configuration to probe.
type target struct {
	Label        string  // "@agent" or "endpoint" for display
	Endpoint     string
	APIKey       string
	Model        string
	SystemPrompt string  // agent prompt (empty for raw endpoint)
	Temperature  float64
}

// resolveTargets picks the target set for a check command.
//
//   - agentRef == "@id":      that agent from the blueprint
//   - agentRef == "" + flags: single target from --endpoint/--key/--model
//   - agentRef == "":         every agent in the blueprint
//
// The blueprint is loaded lazily so ad-hoc --endpoint checks work in a
// directory without one.
func resolveTargets(agentRef string) ([]target, error) {
	flagEndpoint := checkEndpoint != ""

	if agentRef == "" && flagEndpoint {
		return []target{{
			Label:       checkEndpoint,
			Endpoint:    strings.TrimSuffix(checkEndpoint, "/"),
			APIKey:      checkKey,
			Model:       checkModel,
			Temperature: 0.7,
		}}, nil
	}

	bp, err := blueprint.Load(checkFile)
	if err != nil {
		return nil, fmt.Errorf("loading blueprint: %w", err)
	}

	if agentRef != "" {
		for i := range bp.Agents {
			a := &bp.Agents[i]
			if a.ID == agentRef {
				return []target{targetFromAgent(a)}, nil
			}
		}
		return nil, fmt.Errorf("agent %q not found in %s", agentRef, checkFile)
	}

	if len(bp.Agents) == 0 {
		return nil, fmt.Errorf("no agents in blueprint %s", checkFile)
	}
	out := make([]target, 0, len(bp.Agents))
	for i := range bp.Agents {
		a := &bp.Agents[i]
		if a.Type == "acp" {
			// ACP agents don't have an OpenAI endpoint to check.
			continue
		}
		out = append(out, targetFromAgent(a))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no LLM agents in blueprint %s (only ACP agents)", checkFile)
	}
	return out, nil
}

// targetFromAgent builds a target from a blueprint agent, layering flag
// overrides on top. Defaults have already been folded in by blueprint.Load.
func targetFromAgent(a *blueprint.Agent) target {
	endpoint := a.Endpoint
	if checkEndpoint != "" {
		endpoint = checkEndpoint
	}
	apiKey := a.APIKey
	if checkKey != "" {
		apiKey = checkKey
	}
	model := a.Model
	if checkModel != "" {
		model = checkModel
	}
	systemPrompt, err := a.RenderPrompt()
	if err != nil {
		systemPrompt = a.Prompt
	}
	return target{
		Label:        a.ID,
		Endpoint:     strings.TrimSuffix(endpoint, "/"),
		APIKey:       apiKey,
		Model:        model,
		SystemPrompt: systemPrompt,
		Temperature:  a.Temperature,
	}
}

// splitAskArgs peels an optional leading @agent off the arg list and
// joins the rest as the prompt.
func splitAskArgs(args []string) (agentRef, prompt string) {
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		return args[0], strings.Join(args[1:], " ")
	}
	return "", strings.Join(args, " ")
}

func argOrEmpty(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func hasModel(models []llm.Model, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func init() {
	checkCmd.PersistentFlags().StringVarP(&checkFile, "file", "f", "blueprint.yaml", "Blueprint file (for @agent lookups)")
	checkCmd.PersistentFlags().StringVar(&checkEndpoint, "endpoint", "", "OpenAI-compatible base URL (overrides blueprint)")
	checkCmd.PersistentFlags().StringVar(&checkKey, "key", "", "API key (overrides blueprint)")
	checkCmd.PersistentFlags().StringVar(&checkModel, "model", "", "Model name (overrides blueprint)")

	checkCmd.AddCommand(checkPingCmd)
	checkCmd.AddCommand(checkModelsCmd)
	checkCmd.AddCommand(checkAskCmd)

	rootCmd.AddCommand(checkCmd)
}
