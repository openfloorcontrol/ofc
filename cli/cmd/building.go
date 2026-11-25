package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/openfloorcontrol/ofc/api"
	"github.com/openfloorcontrol/ofc/building"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/spf13/cobra"
)

var floorIdleTimeout time.Duration

var buildingCmd = &cobra.Command{
	Use:   "building <dir>",
	Short: "Serve the floors of a building directory in the web UI",
	Long: `Serve the floors of a building directory in the web UI, where floors
can be made from templates, opened, stopped and deleted.

A building directory holds:

  templates/<name>/blueprint.yaml   blueprints floors are made from, with the files they use
  floors/<id>/floor.yaml            a floor: its manifest (id, name, template, created),
  floors/<id>/blueprint.yaml        its own copy of the template, and the rest of it
  sessions/                         the sessions of all floors (unless --db or --sessions-dir)

A floor starts when a request names it and stops after --idle-timeout
without requests, followers or running turns.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runBuilding(args[0])
	},
}

func init() {
	fs := buildingCmd.Flags()
	fs.BoolVar(&debug, "debug", false, "Enable debug output")
	fs.IntVar(&webPort, "port", 8080, "Port for the web UI")
	fs.StringVar(&bindAddr, "bind", "127.0.0.1", "Address the web UI listens on (0.0.0.0 for all interfaces)")
	fs.StringVar(&webHostname, "hostname", "", "External URL for web UI (e.g. https://myhost.dev), overrides localhost in printed URL")
	fs.StringVar(&apiToken, "token", "", "API token (default: a random one)")
	fs.DurationVar(&floorIdleTimeout, "idle-timeout", 30*time.Minute, "Stop a floor after it is idle this long; 0 keeps floors running")
	fs.DurationVar(&acpIdleTimeout, "acp-idle-timeout", 0, "Close an ACP agent's subprocess after it is idle this long (e.g. 15m); 0 keeps it until its session closes")
	fs.StringVar(&deployVersion, "deploy-version", "", "Version reported as \"version\" by GET /api/health")
	addStoreFlags(fs)
	addOAuthFlags(fs)
}

// runBuilding serves the building at dir until interrupted.
func runBuilding(dir string) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", dir)
		os.Exit(1)
	}
	if sessionsDir == "" && dbDSN == "" {
		sessionsDir = filepath.Join(dir, "sessions")
	}
	store, label, err := openSessionStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	srv := api.New()
	b := building.New(dir, srv, store)
	b.IdleTimeout = floorIdleTimeout
	b.Info = func(msg string) { fmt.Println(msg) }
	b.Setup = func(f *floor.Floor) error {
		if debug {
			f.DebugFunc = func(msg string) { fmt.Fprintf(os.Stderr, "[debug] %s: %s\n", f.ID(), msg) }
		}
		if err := configureFloor(f); err != nil {
			return err
		}
		meta, err := makeSessionMeta(f.ID(), f.Blueprint, filepath.Join(f.Blueprint.Dir, "blueprint.yaml"))
		if err != nil {
			return err
		}
		f.SessionMetaTemplate = &meta
		return nil
	}
	srv.SetBuilding(b)
	startWebServer(srv)
	defer srv.Stop()
	defer b.Close()
	fmt.Printf("Building %s, sessions in %s (%s)\n", dir, storeLocation(), label)

	b.StopIdleFloors(min(floorIdleTimeout/4, time.Minute))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	fmt.Println("\nShutting down.")
}

// storeLocation describes where openSessionStore keeps sessions.
func storeLocation() string {
	if dbDSN != "" {
		return "Postgres"
	}
	dir, _ := defaultSessionsDir()
	return dir
}
