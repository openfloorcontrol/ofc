// Package cmd implements the CLI commands for ofc.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version info - set via ldflags at build time
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "ofc",
	Short: "OFC - Open Floor Control",
	Long:  `Compose and run multi-agent teams.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(evalCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(versionCmd)

	sessionsCmd.AddCommand(sessionsLsCmd)
	sessionsCmd.AddCommand(sessionsRmCmd)
	sessionsCmd.AddCommand(sessionsShowCmd)
	sessionsRmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(sessionsCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("ofc %s\n", Version)
		if Version != "dev" {
			fmt.Printf("  commit: %s\n", Commit)
			fmt.Printf("  built:  %s\n", Date)
		}
	},
}
