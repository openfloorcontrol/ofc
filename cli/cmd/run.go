package cmd

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/spf13/cobra"
)

var (
	blueprintFile string
	debug         bool
	logFile       string
	useTUI        bool
	useWeb        bool
	webPort       int
	useJSON       bool
)

var runCmd = &cobra.Command{
	Use:   "run [prompt]",
	Short: "Run a floor",
	Long:  `Run a floor with optional initial prompt.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Load blueprint
		bp, err := blueprint.Load(blueprintFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading blueprint: %v\n", err)
			fmt.Fprintln(os.Stderr, "Create one with: ofc init")
			os.Exit(1)
		}

		var initialPrompt string
		if len(args) > 0 {
			initialPrompt = args[0]
		}

		if useJSON {
			runJSON(bp, initialPrompt)
		} else if useTUI {
			runTUI(bp, initialPrompt)
		} else {
			runCLI(bp, initialPrompt)
		}
	},
}

func runCLI(bp *blueprint.Blueprint, initialPrompt string) {
	cm := floor.BuildColorMap(bp)
	frontend := floor.NewCLIFrontend(logFile, debug, cm)

	f := floor.NewFloor(bp)
	if debug {
		f.DebugFunc = frontend.Debug
	}
	f.LogWriter = frontend.LogWriter()

	if useWeb {
		f.ListenAddr = fmt.Sprintf(":%d", webPort)
		f.ServeWebDist = true
		frontend.Headless = true
	}

	ctrl := floor.NewController(bp)
	if debug {
		ctrl.DebugFunc = frontend.Debug
	}

	agents := buildAgents(bp)

	if err := frontend.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
		if err.Error() != "stop" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func runTUI(bp *blueprint.Blueprint, initialPrompt string) {
	cm := floor.BuildColorMap(bp)
	frontend, model := floor.NewTUIFrontend(logFile, debug, cm)

	f := floor.NewFloor(bp)
	if debug {
		f.DebugFunc = func(msg string) {
			frontend.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}
	f.LogWriter = frontend.LogWriter()

	var stderrWriter io.Writer = io.Discard
	if lw := frontend.LogWriter(); lw != nil {
		stderrWriter = lw
	}
	f.StderrWriter = stderrWriter

	ctrl := floor.NewController(bp)
	if debug {
		ctrl.DebugFunc = func(msg string) {
			frontend.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}

	agents := buildAgents(bp)

	// Set up Bubble Tea
	model.SetChat(f.Chat)
	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	frontend.SetProgram(p)

	// Start the event loop (background goroutine)
	if err := frontend.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Bubble Tea owns the main thread
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func runJSON(bp *blueprint.Blueprint, initialPrompt string) {
	frontend := floor.NewJSONFrontend(logFile, debug)

	f := floor.NewFloor(bp)
	if debug {
		f.DebugFunc = frontend.Debug
	}
	f.LogWriter = frontend.LogWriter()

	ctrl := floor.NewController(bp)
	if debug {
		ctrl.DebugFunc = frontend.Debug
	}

	agents := buildAgents(bp)

	if err := frontend.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
		if err.Error() != "stop" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

// buildAgents creates Agent instances from the blueprint.
func buildAgents(bp *blueprint.Blueprint) map[string]floor.Agent {
	agents := make(map[string]floor.Agent)
	for i := range bp.Agents {
		a := &bp.Agents[i]
		switch a.Type {
		case "acp":
			agents[a.ID] = floor.NewACPAgent(a)
		default:
			agents[a.ID] = floor.NewLLMAgent(a)
		}
	}
	return agents
}

func init() {
	runCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
	runCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug output")
	runCmd.Flags().StringVar(&logFile, "log", "", "Log output to file (plain text, no colors)")
	runCmd.Flags().BoolVar(&useTUI, "tui", false, "Use terminal UI with split layout")
	runCmd.Flags().BoolVar(&useWeb, "web", false, "Enable web UI (serves web/dist/ on --port)")
	runCmd.Flags().IntVar(&webPort, "port", 8080, "Port for web UI (used with --web)")
	runCmd.Flags().BoolVar(&useJSON, "json", false, "Output events as JSONL to stdout")
}
