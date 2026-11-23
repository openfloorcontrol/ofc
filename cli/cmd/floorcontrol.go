package cmd

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/floorcontrol"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var floorControlCmd = &cobra.Command{
	Use:   "floor-control",
	Short: "Work on a blueprint's floor control",
}

var (
	fcBlueprintFile string
	fcModel         string
	fcRuns          int
)

var floorControlEvalCmd = &cobra.Command{
	Use:   "eval <cases.yaml>",
	Short: "Run floor control cases against a model",
	Long: `Run each case of a cases file through the blueprint's floor control model and
compare the answer with the expected one. The blueprint gives the agents,
their turn_prompt and floor_control; --model tries another model.

A cases file:

  cases:
    - name: customer asks Maya by name
      history:                 # oldest first, "@speaker: text"
        - "@user: What do you think about the D750, Maya"
      want: call @maya         # or a list of acceptable answers
    - name: Hiro answered his own question
      history:
        - "@user: Which Nikon do you have?"
        - "@hiro: Just the D750."
      stack: ["@user -> @hiro"] # optional: who is waiting for whom, outermost first
      passed: ["@maya"]         # optional: agents that passed
      want: [nobody, chime @danny]

Exits non-zero when a case misses.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return evalFloorControl(cmd, args[0])
	},
}

func init() {
	addBlueprintFlag(floorControlEvalCmd.Flags(), &fcBlueprintFile)
	floorControlEvalCmd.Flags().StringVar(&fcModel, "model", "", "model reference to use instead of floor_control.model")
	floorControlEvalCmd.Flags().IntVar(&fcRuns, "runs", 1, "how often to run each case")
	floorControlCmd.AddCommand(floorControlEvalCmd)
	rootCmd.AddCommand(floorControlCmd)
}

// fcCase is one case of a cases file.
type fcCase struct {
	Name    string   `yaml:"name"`
	History []string `yaml:"history"`
	Stack   []string `yaml:"stack"`
	Passed  []string `yaml:"passed"`
	Want    answers  `yaml:"want"`
}

// answers is one acceptable answer or a list of them.
type answers []string

func (a *answers) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*a = answers{n.Value}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*a = list
	return nil
}

var (
	historyLine = regexp.MustCompile(`^(@\w+): (.*)$`)
	stackLine   = regexp.MustCompile(`^(@\w+) -> (@\w+)$`)
)

func evalFloorControl(cmd *cobra.Command, casesPath string) error {
	bp, err := blueprint.Load(fcBlueprintFile)
	if err != nil {
		return fmt.Errorf("load blueprint: %w", err)
	}
	fc := bp.FloorControl
	if fcModel != "" {
		m, err := blueprint.ResolveModel(bp, fcModel)
		if err != nil {
			return err
		}
		fc.Endpoint, fc.Model, fc.APIKey, fc.ModelSource = m.Endpoint, m.Model, m.APIKey, m.Source
	}
	if fc.Model == "" {
		return fmt.Errorf("%s has no floor_control.model; give one with --model", fcBlueprintFile)
	}

	cases, err := readCases(casesPath)
	if err != nil {
		return err
	}
	addresser := floorcontrol.New(fc)
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "model: %s\n\n", fc.ModelSource)

	var runs, hits, invalid, retried, failed int
	for _, c := range cases {
		req, err := c.request(bp)
		if err != nil {
			return fmt.Errorf("case %q: %w", c.Name, err)
		}
		for i := 0; i < fcRuns; i++ {
			res := addresser.Decide(req)
			got := strings.TrimSpace(string(res.Address.Kind) + " " + res.Address.AgentID)
			ok := matches(got, c.Want)
			runs++
			if ok {
				hits++
			}
			switch {
			case res.Err != nil:
				failed++
			case !res.Valid:
				invalid++
			}
			if len(res.Answers) > 1 {
				retried++
			}

			mark, note := "✓", ""
			if !ok {
				mark, note = "✗", "  (want "+strings.Join(c.Want, " | ")+")"
			}
			if len(res.Answers) > 1 {
				note += fmt.Sprintf("  [%d attempts]", len(res.Answers))
			}
			fmt.Fprintf(out, "%s %s: %s%s\n", mark, c.Name, got, note)
			if res.Address.Reason != "" {
				fmt.Fprintf(out, "    %s\n", res.Address.Reason)
			}
		}
	}
	fmt.Fprintf(out, "\n%d/%d hits, %d invalid, %d retried, %d failed calls\n", hits, runs, invalid, retried, failed)
	if hits < runs {
		return fmt.Errorf("%d of %d runs missed", runs-hits, runs)
	}
	return nil
}

func readCases(path string) ([]fcCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Cases []fcCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(file.Cases) == 0 {
		return nil, fmt.Errorf("%s has no cases", path)
	}
	return file.Cases, nil
}

// request builds the address request of a case: its messages, call stack,
// and the blueprint's agents that may speak (not "never", not passed).
func (c fcCase) request(bp *blueprint.Blueprint) (floor.AddressRequest, error) {
	req := floor.AddressRequest{DefaultAgent: bp.FloorControl.DefaultAgent}
	if len(c.History) == 0 {
		return req, fmt.Errorf("no history")
	}
	if len(c.Want) == 0 {
		return req, fmt.Errorf("no want")
	}
	for _, h := range c.History {
		m := historyLine.FindStringSubmatch(h)
		if m == nil {
			return req, fmt.Errorf("history line %q: want \"@speaker: text\"", h)
		}
		req.History = append(req.History, floor.ChatMessage{From: m[1], Content: m[2]})
	}
	for _, s := range c.Stack {
		m := stackLine.FindStringSubmatch(s)
		if m == nil {
			return req, fmt.Errorf("stack entry %q: want \"@caller -> @callee\"", s)
		}
		req.CallStack = append(req.CallStack, floor.Frame{Caller: m[1], Callee: m[2]})
	}
	for _, a := range bp.Agents {
		if a.Activation == "never" || contains(c.Passed, a.ID) {
			continue
		}
		req.Agents = append(req.Agents, a)
	}
	return req, nil
}

// matches reports whether got is one of the wanted answers, ignoring case
// and extra spaces.
func matches(got string, want answers) bool {
	for _, w := range want {
		if strings.EqualFold(strings.Join(strings.Fields(w), " "), got) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
