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

		// Get initial prompt if provided
		var initialPrompt string
		if len(args) > 0 {
			initialPrompt = args[0]
		}

		if useTUI {
			runTUI(bp, initialPrompt)
		} else {
			co := floor.NewCoordinator(bp, debug, logFile)
			if err := co.Run(initialPrompt); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	},
}

func runTUI(bp *blueprint.Blueprint, initialPrompt string) {
	frontend, model := floor.NewTUIFrontend(logFile, debug, floor.BuildColorMap(bp))

	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	frontend.SetProgram(p)

	var debugFn func(string)
	if debug {
		debugFn = func(msg string) {
			// In TUI mode, debug goes to log file only
			frontend.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}

	// In TUI mode, route ACP subprocess stderr to log file (or discard)
	// to avoid corrupting the Bubble Tea display.
	var stderrWriter io.Writer = io.Discard
	if lw := frontend.LogWriter(); lw != nil {
		stderrWriter = lw
	}

	co := floor.NewCoordinatorWith(bp, frontend, frontend, debugFn, frontend.LogWriter(), stderrWriter)

	// Run coordinator in background goroutine
	go func() {
		if err := co.Run(initialPrompt); err != nil {
			p.Send(floor.SystemInfo{Text: fmt.Sprintf("[ERROR: %v]", err)})
		}
		// Coordinator finished — quit the TUI
		p.Send(floor.FloorStopped{})
	}()

	// Bubble Tea owns the main thread
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	runCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
	runCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug output")
	runCmd.Flags().StringVar(&logFile, "log", "", "Log output to file (plain text, no colors)")
	runCmd.Flags().BoolVar(&useTUI, "tui", false, "Use terminal UI with split layout")
}
