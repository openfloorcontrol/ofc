package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents"
	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/frontend"
	"github.com/openfloorcontrol/ofc/webui"
	"github.com/spf13/cobra"
)

// attachAPIServer constructs and attaches an api.Server to the floor.
// In web mode it requires a Bearer token: $OFC_TOKEN if set (stable
// across restarts, for clients like `ofc run --remote`), otherwise a
// random one. Called before f.Start.
func attachAPIServer(f *floor.Floor) {
	srv := api.New()
	if useWeb {
		token := os.Getenv("OFC_TOKEN")
		if token == "" {
			token = api.GenerateToken()
		}
		srv.SetAuthToken(token)
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
	sessionID     string
	remoteURL     string
	dbDSN         string

	// resolvedSessionID is the actual UUID used by this invocation —
	// either passed via --session, or freshly generated. Captured here
	// so all frontends and applySessionStore can read it without re-doing
	// resolution.
	resolvedSessionID string
)

var runCmd = &cobra.Command{
	Use:   "run [prompt]",
	Short: "Run a floor",
	Long:  `Run a floor with optional initial prompt.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if remoteURL != "" {
			if useWeb || useTUI {
				fmt.Fprintln(os.Stderr, "Error: --remote works with the CLI and --json frontends")
				os.Exit(1)
			}
			var prompt string
			if len(args) > 0 {
				prompt = args[0]
			}
			runRemote(prompt)
			return
		}

		// Load blueprint
		bp, err := blueprint.Load(blueprintFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading blueprint: %v\n", err)
			// Only suggest scaffolding one when there isn't a blueprint to
			// begin with — for a blueprint that exists but is misconfigured,
			// "ofc init" is a red herring.
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "Create one with: ofc init")
			}
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

		if useWeb {
			if initialPrompt != "" {
				fmt.Fprintln(os.Stderr, "Error: --web takes no prompt; each browser tab starts its own session")
				os.Exit(1)
			}
			runWeb(bp)
		} else if useJSON {
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
	fe := frontend.NewCLI(logFile, debug, frontend.BuildColorMap(agentIDs(bp)))

	f, _ := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = fe.Debug
	}
	f.LogWriter = fe.LogWriter()
	attachAPIServer(f)
	if debug {
		f.DefaultSession().Controller.DebugFunc = fe.Debug
	}

	client, err := startLocalSession(f, fe.RenderInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer f.Stop()

	if err := fe.RunLoop(client, initialPrompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runRemote runs the CLI or JSON frontend against a session on the ofc
// web server at --remote, without a local floor.
func runRemote(initialPrompt string) {
	client, err := openRemoteSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if useJSON {
		err = frontend.NewJSON(logFile, debug).RunLoop(client, initialPrompt)
	} else {
		fmt.Fprintf(os.Stderr, "Session: %s (remote)\n", client.ID())
		fe := frontend.NewCLI(logFile, debug, frontend.BuildColorMap(client.Info().Agents))
		err = fe.RunLoop(client, initialPrompt)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// openRemoteSession connects to the server named by --remote. The URL
// may carry ?token= and ?session= (as printed by `ofc run --web`); the
// token otherwise comes from $OFC_TOKEN, the session from --session.
// Without a session, a new one is started.
func openRemoteSession() (frontend.SessionClient, error) {
	u, err := url.Parse(remoteURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("--remote needs a URL like https://host:port, got %q", remoteURL)
	}
	q := u.Query()
	token := q.Get("token")
	if token == "" {
		token = os.Getenv("OFC_TOKEN")
	}
	sid := q.Get("session")
	if sid == "" {
		sid = sessionID
	}
	base := u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, "/")
	return frontend.NewRemoteSession(base, token, sid)
}

// startLocalSession starts the floor and its default session, subscribed
// before the session starts so no event is missed. The caller stops the
// floor.
func startLocalSession(f *floor.Floor, renderInfo func(string)) (frontend.SessionClient, error) {
	if err := f.Start(renderInfo); err != nil {
		return nil, err
	}
	sess := f.DefaultSession()
	client := frontend.NewLocalSession(f, sess)
	sess.Start()
	return client, nil
}

func agentIDs(bp *blueprint.Blueprint) []string {
	ids := make([]string, len(bp.Agents))
	for i, a := range bp.Agents {
		ids[i] = a.ID
	}
	return ids
}

// runWeb serves the web UI and API until interrupted. Each browser tab
// works in its own session; with --session, that session is resumed and
// its URL printed.
func runWeb(bp *blueprint.Blueprint) {
	f, resuming := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = func(msg string) { fmt.Fprintf(os.Stderr, "[debug] %s\n", msg) }
	}
	f.ListenAddr = fmt.Sprintf(":%d", webPort)
	f.WebMode = true
	f.WebUI = webui.FS()
	f.ExternalURL = webHostname
	attachAPIServer(f)

	if err := f.Start(func(msg string) { fmt.Println(msg) }); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer f.Stop()

	if resuming {
		sess := f.DefaultSession()
		sess.Start()
		base := f.APIServer.BaseURL()
		if webHostname != "" {
			base = strings.TrimSuffix(webHostname, "/")
		}
		fmt.Printf("Resumed session at %s?token=%s&session=%s\n", base, f.APIServer.AuthToken(), sess.ID())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	fmt.Println("\nShutting down.")
}

func runTUI(bp *blueprint.Blueprint, initialPrompt string) {
	fe, model := frontend.NewTUI(logFile, debug, frontend.BuildColorMap(agentIDs(bp)))

	f, _ := newFloorWithStore(bp)
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
	if debug {
		f.DefaultSession().Controller.DebugFunc = func(msg string) {
			fe.Render(floor.SystemInfo{Text: "[debug] " + msg})
		}
	}

	// Set up Bubble Tea
	model.SetChat(f.DefaultSession().MainRoom)
	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	fe.SetProgram(p)

	// Start the event loop (background goroutine)
	if err := fe.RunLoop(f, initialPrompt); err != nil {
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

	f, _ := newFloorWithStore(bp)
	if debug {
		f.DebugFunc = fe.Debug
	}
	f.LogWriter = fe.LogWriter()
	attachAPIServer(f)
	if debug {
		f.DefaultSession().Controller.DebugFunc = fe.Debug
	}

	client, err := startLocalSession(f, fe.EmitInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer f.Stop()

	if err := fe.RunLoop(client, initialPrompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	runCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
	runCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug output")
	runCmd.Flags().StringVar(&logFile, "log", "", "Log output to file (plain text, no colors)")
	runCmd.Flags().BoolVar(&useTUI, "tui", false, "Use terminal UI with split layout")
	runCmd.Flags().BoolVar(&useWeb, "web", false, "Enable web UI (served on --port)")
	runCmd.Flags().IntVar(&webPort, "port", 8080, "Port for web UI (used with --web)")
	runCmd.Flags().StringVar(&webHostname, "hostname", "", "External URL for web UI (e.g. https://myhost.dev), overrides localhost in printed URL")
	runCmd.Flags().BoolVar(&useJSON, "json", false, "Output events as JSONL to stdout")
	runCmd.Flags().StringVar(&sessionID, "session", "", "Session UUID to resume (default: generate a new one)")
	runCmd.Flags().StringVar(&remoteURL, "remote", "", "Use a session on a running `ofc run --web` server at this URL (token from ?token= or $OFC_TOKEN) instead of a local floor")
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
func newFloorWithStore(bp *blueprint.Blueprint) (f *floor.Floor, resuming bool) {
	sid, resuming := resolveSessionID()
	f = floor.NewFloorWithSession(bp, sid)
	f.AgentFactory = agents.New
	oauthDir, err := defaultOAuthDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	f.OAuthDir = oauthDir
	if interactive() {
		f.OAuthConsent = func(name, authURL string) {
			fmt.Fprintf(os.Stderr, "\n%s needs authorization. Open this URL to continue:\n  %s\n\n", name, authURL)
		}
	}
	if err := applySessionStore(f, bp, resuming); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return f, resuming
}

// applySessionStore picks the session-store backend for this
// invocation. --db (or OFC_DATABASE_URL) selects Postgres; otherwise
// JSONL. Either way SessionMeta is written on fresh sessions and
// checked on resume. The session UUID is f.DefaultSessionID() —
// shared across both backends.
func applySessionStore(f *floor.Floor, bp *blueprint.Blueprint, resuming bool) error {
	store, label, err := openSessionStore()
	if err != nil {
		return err
	}
	f.Store = store
	sid := f.DefaultSessionID()

	// Meta is for hygiene, not correctness: failing to build it is a warning.
	meta, metaErr := makeSessionMeta(bp, blueprintFile)
	if metaErr != nil {
		fmt.Fprintf(os.Stderr, "[warning] could not record session meta: %v\n", metaErr)
	} else {
		f.SessionMetaTemplate = &meta
	}

	// In web mode without --session the default session goes unused —
	// browsers create their own — so nothing is recorded for it.
	unused := useWeb && !resuming

	if !useJSON && !unused {
		if resuming {
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
	} else if !unused && metaErr == nil {
		if err := store.SetMeta(sid, meta); err != nil {
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

