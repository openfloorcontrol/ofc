package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
	"github.com/spf13/cobra"
)

var (
	rmForce bool
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage persisted sessions",
	Long:  `List, show, and remove sessions stored under ~/.ofc/sessions (or $OFC_SESSIONS_DIR).`,
}

var sessionsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List sessions in the default sessions directory",
	Run: func(cmd *cobra.Command, args []string) {
		dir, err := defaultSessionsDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No sessions yet.")
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", dir, err)
			os.Exit(1)
		}

		type sessionRow struct {
			id        string
			path      string
			info      os.FileInfo
			mtime     int64
			blueprint string // from meta if present, "" if unknown
			cwdShort  string // last path segment for compactness
		}
		var rows []sessionRow
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			info, err := e.Info()
			if err != nil {
				continue
			}
			row := sessionRow{
				id:    id,
				path:  filepath.Join(dir, name),
				info:  info,
				mtime: info.ModTime().Unix(),
			}
			// Best-effort: read meta to enrich the listing. Skip on error.
			if meta, err := readSessionMeta(row.path); err == nil {
				row.blueprint = meta.BlueprintName
				row.cwdShort = shortCWD(meta.CWD)
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			fmt.Println("No sessions yet.")
			return
		}

		// Sort newest first
		sort.Slice(rows, func(i, j int) bool { return rows[i].mtime > rows[j].mtime })

		// Tab-style columns. Header + rows.
		// Truncate UUID to 8 chars for readability — full UUID still works as input.
		fmt.Printf("%-8s  %-19s  %-8s  %-20s  %s\n", "UUID", "LAST MODIFIED", "SIZE", "BLUEPRINT", "CWD")
		for _, r := range rows {
			bp := r.blueprint
			if bp == "" {
				bp = "-"
			}
			cwd := r.cwdShort
			if cwd == "" {
				cwd = "-"
			}
			fmt.Printf("%-8s  %-19s  %-8s  %-20s  %s\n",
				r.id[:8],
				r.info.ModTime().Format("2006-01-02 15:04:05"),
				humanSize(r.info.Size()),
				truncate(bp, 20),
				cwd)
		}
	},
}

var sessionsRmCmd = &cobra.Command{
	Use:   "rm <uuid>",
	Short: "Remove a session file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		path, err := sessionPath(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Session %s not found\n", id)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

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

		if err := os.Remove(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting %s: %v\n", path, err)
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
		path, err := sessionPath(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Session %s not found\n", id)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		store, err := sessionstore.NewJSONL(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		fmt.Printf("# Session %s\n\n", id)

		// Print meta header if recorded.
		if meta, err := store.GetMeta("default"); err == nil {
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

		// Files written by ofc run use "default" as the internal session
		// id. (The file name is the externally-visible UUID.)
		events, err := store.Read("default", floor.EventFilter{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading session: %v\n", err)
			os.Exit(1)
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

// readSessionMeta opens a JSONL store at the given path, fetches the
// stored meta (if any), and closes. Used by `ofc sessions ls` to enrich
// the listing without loading the full session into memory long-term.
func readSessionMeta(path string) (floor.SessionMeta, error) {
	store, err := sessionstore.NewJSONL(path)
	if err != nil {
		return floor.SessionMeta{}, err
	}
	defer store.Close()
	return store.GetMeta("default")
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

// humanSize formats a byte count as a short human-readable string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), suffix)
}
