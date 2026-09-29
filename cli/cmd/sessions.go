package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openfloorcontrol/ofc/floor"
	"github.com/spf13/cobra"
)

var (
	rmForce bool
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage persisted sessions",
	Long: `List, show, and remove sessions in the session store: Postgres if --db or
$OFC_DATABASE_URL is set, otherwise ~/.ofc/sessions (or $OFC_SESSIONS_DIR).`,
}

var sessionsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List sessions, most recently active first",
	Run: func(cmd *cobra.Command, args []string) {
		store := mustOpenSessionStore()
		defer store.Close()

		infos, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing sessions: %v\n", err)
			os.Exit(1)
		}
		if len(infos) == 0 {
			fmt.Println("No sessions yet.")
			return
		}

		// Truncate UUID to 8 chars for readability — full UUID still works as input.
		fmt.Printf("%-8s  %-19s  %6s  %-20s  %s\n", "UUID", "LAST ACTIVITY", "EVENTS", "BLUEPRINT", "CWD")
		for _, info := range infos {
			last := "-"
			if !info.LastActivity.IsZero() {
				last = info.LastActivity.Local().Format("2006-01-02 15:04:05")
			}
			bp, cwd := "-", "-"
			if info.Meta != nil {
				if info.Meta.BlueprintName != "" {
					bp = info.Meta.BlueprintName
				}
				if info.Meta.CWD != "" {
					cwd = shortCWD(info.Meta.CWD)
				}
			}
			fmt.Printf("%-8s  %-19s  %6d  %-20s  %s\n",
				info.ID[:min(8, len(info.ID))], last, info.EventCount, truncate(bp, 20), cwd)
		}
	},
}

var sessionsRmCmd = &cobra.Command{
	Use:   "rm <uuid>",
	Short: "Remove a session",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		store := mustOpenSessionStore()
		defer store.Close()

		if !rmForce {
			fmt.Printf("Delete session %s? [y/N] ", id)
			reader := bufio.NewReader(os.Stdin)
			ans, _ := reader.ReadString('\n')
			ans = strings.TrimSpace(strings.ToLower(ans))
			if ans != "y" && ans != "yes" {
				fmt.Println("Cancelled.")
				return
			}
		}

		if err := store.Delete(id); err != nil {
			if errors.Is(err, floor.ErrSessionNotFound) {
				fmt.Fprintf(os.Stderr, "Session %s not found\n", id)
			} else {
				fmt.Fprintf(os.Stderr, "Error deleting session %s: %v\n", id, err)
			}
			os.Exit(1)
		}
		fmt.Printf("Deleted %s\n", id)
	},
}

var sessionsShowCmd = &cobra.Command{
	Use:   "show <uuid>",
	Short: "Print a session's conversation as markdown",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		store := mustOpenSessionStore()
		defer store.Close()

		meta, metaErr := store.GetMeta(id)
		if metaErr != nil && !errors.Is(metaErr, floor.ErrNoSessionMeta) {
			fmt.Fprintf(os.Stderr, "Error reading session meta: %v\n", metaErr)
			os.Exit(1)
		}
		events, err := store.Read(id, floor.EventFilter{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading session: %v\n", err)
			os.Exit(1)
		}
		if metaErr != nil && len(events) == 0 {
			fmt.Fprintf(os.Stderr, "Session %s not found\n", id)
			os.Exit(1)
		}

		fmt.Printf("# Session %s\n\n", id)

		if metaErr == nil {
			fmt.Printf("- **Blueprint**: %s\n", meta.BlueprintName)
			if meta.BlueprintPath != "" {
				fmt.Printf("- **Blueprint path**: %s\n", meta.BlueprintPath)
			}
			if meta.CWD != "" {
				fmt.Printf("- **Working dir**: %s\n", meta.CWD)
			}
			if !meta.CreatedAt.IsZero() {
				fmt.Printf("- **Created**: %s\n", meta.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			if meta.OfcVersion != "" {
				fmt.Printf("- **ofc version**: %s\n", meta.OfcVersion)
			}
			fmt.Println()
		}

		if len(events) == 0 {
			fmt.Println("_(no messages)_")
			return
		}
		for _, ev := range events {
			mp, ok := ev.Event.(floor.MessagePostedEvent)
			if !ok {
				continue
			}
			msg := mp.Message
			fmt.Printf("**[%s]**: %s\n\n", msg.From, msg.Content)
		}
	},
}

// mustOpenSessionStore opens the configured store or exits.
func mustOpenSessionStore() sessionStore {
	store, _, err := openSessionStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return store
}

// shortCWD returns the last 2 path segments of a directory, prefixed
// with "…/" if there are more. For listings.
func shortCWD(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(p, string(filepath.Separator))
	if len(parts) <= 2 {
		return p
	}
	return "…/" + strings.Join(parts[len(parts)-2:], string(filepath.Separator))
}

// truncate caps s at n runes, appending "…" if cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
