package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	acpagent "github.com/openfloorcontrol/ofc/floor/agents/acp"
	llmagent "github.com/openfloorcontrol/ofc/floor/agents/llm"
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
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
	if err := applySessionLog(f, bp); err != nil {
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
	if err := applySessionLog(f, bp); err != nil {
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
	if err := applySessionLog(f, bp); err != nil {
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
			agents[a.ID] = acpagent.New(a)
		default:
			agents[a.ID] = llmagent.New(a)
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

// internalSessionID is the key used inside a JSONL file for the session's
// events and meta. The externally-meaningful identifier is the UUID
// (the file name); internally we use "default" so multiple files don't
// confuse each other at the record level.
const internalSessionID = "default"

// applySessionLog overrides Floor.Store with a JSONLStore at the resolved
// path. Prints a "Session: <uuid>" line so the user knows what to
// reference later. On a fresh session, records SessionMeta (cwd,
// blueprint path, name, hash, version, time). On resume, fetches
// existing meta and warns on any mismatch.
func applySessionLog(f *floor.Floor, bp *blueprint.Blueprint) error {
	path, resuming, err := resolveSessionPath()
	if err != nil {
		return err
	}
	store, err := sessionstore.NewJSONL(path)
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}
	f.Store = store

	if !useJSON {
		if sessionLog != "" {
			fmt.Fprintf(os.Stderr, "Session log: %s\n", sessionLog)
		} else if resuming {
			fmt.Fprintf(os.Stderr, "Resuming session %s\n", resolvedSessionID)
		} else {
			fmt.Fprintf(os.Stderr, "Session: %s\n", resolvedSessionID)
		}
	}

	if resuming {
		if existing, err := store.GetMeta(internalSessionID); err == nil {
			warnOnMetaMismatch(existing, bp, blueprintFile)
		}
		// If no meta recorded (older file), stay silent.
	} else {
		meta, err := makeSessionMeta(bp, blueprintFile)
		if err != nil {
			// Non-fatal — meta is for hygiene, not correctness.
			fmt.Fprintf(os.Stderr, "[warning] could not record session meta: %v\n", err)
		} else if err := store.SetMeta(internalSessionID, meta); err != nil {
			fmt.Fprintf(os.Stderr, "[warning] could not write session meta: %v\n", err)
		}
	}
	return nil
}

// makeSessionMeta builds a SessionMeta for the current invocation. The
// blueprint hash is sha256 of the on-disk file contents (captures any
// change — prompts, agent set, furniture config).
func makeSessionMeta(bp *blueprint.Blueprint, bpPath string) (floor.SessionMeta, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // not fatal
	}
	absPath, err := filepath.Abs(bpPath)
	if err != nil {
		absPath = bpPath
	}
	hash, err := hashFile(absPath)
	if err != nil {
		return floor.SessionMeta{}, fmt.Errorf("hash blueprint: %w", err)
	}
	return floor.SessionMeta{
		CWD:           cwd,
		BlueprintPath: absPath,
		BlueprintName: bp.Name,
		BlueprintHash: hash,
		OfcVersion:    Version,
		CreatedAt:     time.Now(),
	}, nil
}

// hashFile returns the hex-encoded sha256 of a file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// warnOnMetaMismatch compares the resumed session's recorded meta to the
// current invocation's context. Each mismatch becomes a stderr warning;
// none of them block the resume — they're for the user to decide if the
// drift matters.
func warnOnMetaMismatch(existing floor.SessionMeta, bp *blueprint.Blueprint, bpPath string) {
	// CWD comparison
	cwd, _ := os.Getwd()
	if existing.CWD != "" && cwd != "" && existing.CWD != cwd {
		fmt.Fprintf(os.Stderr, "[warning] session was started in %s; current dir is %s\n", existing.CWD, cwd)
	}

	// Blueprint path comparison
	absPath, err := filepath.Abs(bpPath)
	if err != nil {
		absPath = bpPath
	}
	if existing.BlueprintPath != "" && existing.BlueprintPath != absPath {
		fmt.Fprintf(os.Stderr, "[warning] session was started with blueprint %s; now using %s\n", existing.BlueprintPath, absPath)
	}

	// Blueprint name comparison (catches a renamed blueprint file)
	if existing.BlueprintName != "" && existing.BlueprintName != bp.Name {
		fmt.Fprintf(os.Stderr, "[warning] blueprint name changed: %q → %q\n", existing.BlueprintName, bp.Name)
	}

	// Blueprint content hash
	currentHash, err := hashFile(absPath)
	if err == nil && existing.BlueprintHash != "" && existing.BlueprintHash != currentHash {
		fmt.Fprintf(os.Stderr, "[warning] blueprint file contents changed since session started (hash mismatch)\n")
	}

	// Version comparison (informational)
	if existing.OfcVersion != "" && existing.OfcVersion != Version && Version != "dev" {
		fmt.Fprintf(os.Stderr, "[info] session was created with ofc %s; running %s\n", existing.OfcVersion, Version)
	}
}

