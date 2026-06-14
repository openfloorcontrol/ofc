// Package cmd implements the CLI commands for ofc.
package cmd

import (
	"fmt"
	rdebug "runtime/debug"

	"github.com/spf13/cobra"
)

// Version info - set via ldflags at build time, or auto-detected from
// the Go build info embedded by `go build` (VCS revision + dirty bit).
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
	fillBuildInfo()

	rootCmd.Version = fmt.Sprintf("%s (commit %s)", Version, Commit)
	rootCmd.SetHelpTemplate(`{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}
ofc ` + Version + ` (commit ` + Commit + `)
`)

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

// fillBuildInfo populates Commit (and Date when missing) from the Go
// build info embedded by `go build`. Only runs when ldflags haven't
// already set them — so release builds keep their injected values.
func fillBuildInfo() {
	if Commit != "none" {
		return
	}
	info, ok := rdebug.ReadBuildInfo()
	if !ok {
		return
	}
	var rev, when string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		case "vcs.time":
			when = s.Value
		}
	}
	if rev == "" {
		return
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	Commit = rev
	if Date == "unknown" && when != "" {
		Date = when
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("ofc %s\n", Version)
		fmt.Printf("  commit: %s\n", Commit)
		fmt.Printf("  built:  %s\n", Date)
	},
}
