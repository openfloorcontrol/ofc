package cmd

import (
	"context"
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
	"github.com/openfloorcontrol/ofc/floor/agents"
	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/frontend"
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
	"github.com/spf13/cobra"
)

// attachAPIServer constructs and attaches an api.Server to the floor.
// If web mode is requested, also generates an auth token. Called by
// every runCLI/runTUI/runJSON before f.Start.
func attachAPIServer(f *floor.Floor) {
	srv := api.New()
	if useWeb {
		srv.SetAuthToken(api.GenerateToken())
	}
	f.APIServer = srv
}

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
	dbDSN         string

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

		// Apply blueprint config to flag globals where the flag wasn't
		// explicitly passed. CLI flag wins over Config; Config wins over
		// the cobra-registered default.
		applyBlueprintConfig(cmd, &bp.Config)

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

// applyBlueprintConfig copies non-empty values from bp.Config into the
// flag globals — but only when the corresponding flag wasn't explicitly
// passed on the command line. Frontend selection ("cli"/"tui"/"json")
// is translated into the boolean flag globals the runCmd dispatch
// reads. cobra's Changed() lets us tell "user didn't pass --debug" from
// "user passed --debug=false".
func applyBlueprintConfig(cmd *cobra.Command, cfg *blueprint.Config) {
	fs := cmd.Flags()
	if !fs.Changed("debug") && cfg.Debug {
		debug = true
	}
	if !fs.Changed("log") && cfg.Log != "" {
		logFile = cfg.Log
	}
	if !fs.Changed("web") && cfg.Web.Enabled {
		useWeb = true
	}
	if !fs.Changed("port") && cfg.Web.Port != 0 {
		webPort = cfg.Web.Port
	}
	if !fs.Changed("hostname") && cfg.Web.Hostname != "" {
		webHostname = cfg.Web.Hostname
	}
	if !fs.Changed("db") && cfg.Store.Type == "postgres" && cfg.Store.DSN != "" {
		dbDSN = cfg.Store.DSN
	}
	switch cfg.Frontend {
	case "tui":
		if !fs.Changed("tui") {
			useTUI = true
		}
	case "json":
		if !fs.Changed("json") {
			useJSON = true
		}
	case "cli", "":
		// default — nothing to do
	}
}

func runCLI(bp *blueprint.Blueprint, initialPrompt string) {
	cm := frontend.BuildColorMap(bp)
	fe := frontend.NewCLI(logFile, debug, cm)

	f := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = fe.Debug
	}
	f.LogWriter = fe.LogWriter()

	if useWeb {
		f.ListenAddr = fmt.Sprintf(":%d", webPort)
		f.ServeWebDist = true
		f.ExternalURL = webHostname
		fe.Headless = true
	}
	attachAPIServer(f)

	ctrl := floor.NewController(f)
	if debug {
		ctrl.DebugFunc = fe.Debug
	}

	agents := buildAgents(bp)

	if err := fe.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
		if err.Error() != "stop" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func runTUI(bp *blueprint.Blueprint, initialPrompt string) {
	cm := frontend.BuildColorMap(bp)
	fe, model := frontend.NewTUI(logFile, debug, cm)

	f := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = func(msg string) {
			fe.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}
	f.LogWriter = fe.LogWriter()

	var stderrWriter io.Writer = io.Discard
	if lw := fe.LogWriter(); lw != nil {
		stderrWriter = lw
	}
	f.StderrWriter = stderrWriter
	attachAPIServer(f)

	ctrl := floor.NewController(f)
	if debug {
		ctrl.DebugFunc = func(msg string) {
			fe.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}

	agents := buildAgents(bp)

	// Set up Bubble Tea
	model.SetChat(f.DefaultSession().MainRoom)
	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	fe.SetProgram(p)

	// Start the event loop (background goroutine)
	if err := fe.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
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
	fe := frontend.NewJSON(logFile, debug)

	f := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = fe.Debug
	}
	f.LogWriter = fe.LogWriter()
	attachAPIServer(f)

	ctrl := floor.NewController(f)
	if debug {
		ctrl.DebugFunc = fe.Debug
	}

	agents := buildAgents(bp)

	if err := fe.RunLoop(f, ctrl, agents, initialPrompt); err != nil {
		if err.Error() != "stop" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

// buildAgents creates Agent instances from the blueprint.
func buildAgents(bp *blueprint.Blueprint) map[string]floor.Agent {
	out := make(map[string]floor.Agent)
	for i := range bp.Agents {
		a := &bp.Agents[i]
		switch a.Type {
		case "acp":
			out[a.ID] = agents.NewACP(a)
		default:
			out[a.ID] = agents.NewLLM(a)
		}
	}
	return out
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
	runCmd.Flags().StringVar(&dbDSN, "db", "", "Postgres DSN for session storage (overrides JSONL; falls back to OFC_DATABASE_URL)")
}

// resolveSessionID picks the floor's session UUID for this invocation:
//   - --session <uuid>: that UUID, marked as resuming
//   - otherwise:        a fresh UUID
//
// Sets resolvedSessionID as a side effect so frontends can print it.
func resolveSessionID() (sid string, resuming bool) {
	if sessionID == "" {
		sessionID = uuid.NewString()
	} else {
		resuming = true
	}
	resolvedSessionID = sessionID
	return sessionID, resuming
}

// newFloorWithStore resolves the session UUID, constructs the Floor
// with that UUID as its default session, and attaches the configured
// session store (Postgres if --db / OFC_DATABASE_URL, else JSONL). On
// error it prints to stderr and exits — every caller (runCLI, runTUI,
// runJSON) handles failure the same way.
func newFloorWithStore(bp *blueprint.Blueprint) *floor.Floor {
	sid, resuming := resolveSessionID()
	f := floor.NewFloorWithSession(bp, sid)
	if err := applySessionStore(f, bp, resuming); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return f
}

// applySessionStore picks the session-store backend for this
// invocation. --db (or OFC_DATABASE_URL) selects Postgres; otherwise
// JSONL. Either way SessionMeta is written on fresh sessions and
// checked on resume. The session UUID is f.DefaultSessionID() —
// shared across both backends.
func applySessionStore(f *floor.Floor, bp *blueprint.Blueprint, resuming bool) error {
	dsn := dbDSN
	if dsn == "" {
		dsn = os.Getenv("OFC_DATABASE_URL")
	}

	var store floor.SessionStore
	var label string
	if dsn != "" {
		pg, err := sessionstore.OpenPostgres(context.Background(), dsn)
		if err != nil {
			return fmt.Errorf("session store: %w", err)
		}
		store = pg
		label = "postgres"
	} else {
		path, err := jsonlPathForSession(sessionID)
		if err != nil {
			return err
		}
		jl, err := sessionstore.NewJSONL(path)
		if err != nil {
			return fmt.Errorf("session store: %w", err)
		}
		store = jl
		label = "jsonl"
	}
	f.Store = store
	sid := f.DefaultSessionID()

	if !useJSON {
		if sessionLog != "" {
			fmt.Fprintf(os.Stderr, "Session log: %s\n", sessionLog)
		} else if resuming {
			fmt.Fprintf(os.Stderr, "Resuming session %s (%s)\n", sid, label)
		} else {
			fmt.Fprintf(os.Stderr, "Session: %s (%s)\n", sid, label)
		}
	}

	if resuming {
		if existing, err := store.GetMeta(sid); err == nil {
			warnOnMetaMismatch(existing, bp, blueprintFile)
		}
		// If no meta recorded (older file/row), stay silent.
	} else {
		meta, err := makeSessionMeta(bp, blueprintFile)
		if err != nil {
			// Non-fatal — meta is for hygiene, not correctness.
			fmt.Fprintf(os.Stderr, "[warning] could not record session meta: %v\n", err)
		} else if err := store.SetMeta(sid, meta); err != nil {
			fmt.Fprintf(os.Stderr, "[warning] could not write session meta: %v\n", err)
		}
	}
	return nil
}

// jsonlPathForSession returns the on-disk JSONL path for a session UUID.
// Honors --session-log <path> as an explicit override.
func jsonlPathForSession(sid string) (string, error) {
	if sessionLog != "" {
		return sessionLog, nil
	}
	return sessionPath(sid)
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

