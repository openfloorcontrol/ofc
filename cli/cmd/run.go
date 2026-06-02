package cmd

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
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
	webHostname   string
	useJSON       bool
	sessionLog    string
	sessionID     string

	// resolvedSessionID is the actual UUID used by this invocation —
	// either passed via --session, or freshly generated. Captured here
	// so all frontends and applySessionLog can read it without re-doing
	// resolution.
	resolvedSessionID string
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
	if err := applySessionLog(f); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if debug {
		f.DebugFunc = frontend.Debug
	}
	f.LogWriter = frontend.LogWriter()

	if useWeb {
		f.ListenAddr = fmt.Sprintf(":%d", webPort)
		f.ServeWebDist = true
		f.ExternalURL = webHostname
		frontend.Headless = true
	}

	ctrl := floor.NewController(f)
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
	if err := applySessionLog(f); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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

	ctrl := floor.NewController(f)
	if debug {
		ctrl.DebugFunc = func(msg string) {
			frontend.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}

	agents := buildAgents(bp)

	// Set up Bubble Tea
	model.SetChat(f.DefaultSession().MainRoom)
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
	if err := applySessionLog(f); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if debug {
		f.DebugFunc = frontend.Debug
	}
	f.LogWriter = frontend.LogWriter()

	ctrl := floor.NewController(f)
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
	runCmd.Flags().StringVar(&webHostname, "hostname", "", "External URL for web UI (e.g. https://myhost.dev), overrides localhost in printed URL")
	runCmd.Flags().BoolVar(&useJSON, "json", false, "Output events as JSONL to stdout")
	runCmd.Flags().StringVar(&sessionLog, "session-log", "", "Persist session events to a JSONL file (explicit path, overrides --session)")
	runCmd.Flags().StringVar(&sessionID, "session", "", "Session UUID to resume (default: generate a new one)")
}

// resolveSessionPath picks the JSONL path for this invocation:
//   - --session-log <path>: explicit, used as-is. Caller responsible for paths.
//   - --session <uuid>:     resolves to default sessions dir + uuid.jsonl
//   - neither:              generates a fresh UUID, uses default sessions dir
//
// Sets resolvedSessionID as a side effect so callers can print it.
// Returns ("", false, err) on error, ("", false, nil) if no persistence
// is configured (impossible today since we default to UUID).
func resolveSessionPath() (string, bool, error) {
	if sessionLog != "" {
		// Explicit path. The session UUID inside the JSONL file is
		// whatever's there (or "default" for our hardcoded sessionID
		// at this layer); we don't surface it.
		resolvedSessionID = sessionLog
		return sessionLog, false, nil
	}

	var resuming bool
	if sessionID == "" {
		sessionID = uuid.NewString()
	} else {
		resuming = true
	}
	resolvedSessionID = sessionID

	path, err := sessionPath(sessionID)
	if err != nil {
		return "", false, err
	}
	return path, resuming, nil
}

// applySessionLog overrides Floor.Store with a JSONLStore at the resolved
// path. Prints a "Session: <uuid>" line so the user knows what to
// reference later.
func applySessionLog(f *floor.Floor) error {
	path, resuming, err := resolveSessionPath()
	if err != nil {
		return err
	}
	store, err := floor.NewJSONLStore(path)
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}
	f.Store = store

	// Print a session line so users can grep / copy the UUID. For
	// --session-log, we echo the path instead. JSON frontend skips
	// this — it's noise in machine-readable output.
	if !useJSON {
		if sessionLog != "" {
			fmt.Fprintf(os.Stderr, "Session log: %s\n", sessionLog)
		} else if resuming {
			fmt.Fprintf(os.Stderr, "Resuming session %s\n", resolvedSessionID)
		} else {
			fmt.Fprintf(os.Stderr, "Session: %s\n", resolvedSessionID)
		}
	}
	return nil
}
