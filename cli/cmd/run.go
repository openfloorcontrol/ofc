package cmd

import (
	"fmt"
	"os"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/spf13/cobra"
)

var (
	blueprintFile string
	debug         bool
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

		// Run the floor
		f := floor.New(bp, debug)
		if err := f.Run(initialPrompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	runCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
	runCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug output")
}
