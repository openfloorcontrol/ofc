package cmd

import (
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/spf13/cobra"
)

// withFlagDefaults resets the run-command flag globals to their
// zero/default values and returns a cleanup func. Tests mutate package
// globals (flags are wired to them), so isolation matters.
func withFlagDefaults(t *testing.T) {
	t.Helper()
	prevDebug, prevLog := debug, logFile
	prevTUI, prevWeb := useTUI, useWeb
	prevPort, prevHost := webPort, webHostname
	prevJSON, prevDSN := useJSON, dbDSN

	debug, logFile = false, ""
	useTUI, useWeb = false, false
	webPort, webHostname = 8080, ""
	useJSON, dbDSN = false, ""

	t.Cleanup(func() {
		debug, logFile = prevDebug, prevLog
		useTUI, useWeb = prevTUI, prevWeb
		webPort, webHostname = prevPort, prevHost
		useJSON, dbDSN = prevJSON, prevDSN
	})
}

// newRunCmdForTest builds a minimal cobra.Command with the same flag
// surface as runCmd, wired to the package globals. applyBlueprintConfig
// reads .Flags().Changed(...), so we need a real Command — but we
// don't need its Run.
func newRunCmdForTest() *cobra.Command {
	c := &cobra.Command{Use: "run"}
	c.Flags().BoolVar(&debug, "debug", false, "")
	c.Flags().StringVar(&logFile, "log", "", "")
	c.Flags().BoolVar(&useTUI, "tui", false, "")
	c.Flags().BoolVar(&useWeb, "web", false, "")
	c.Flags().IntVar(&webPort, "port", 8080, "")
	c.Flags().StringVar(&webHostname, "hostname", "", "")
	c.Flags().BoolVar(&useJSON, "json", false, "")
	c.Flags().StringVar(&dbDSN, "db", "", "")
	return c
}

func TestApplyBlueprintConfig_FillsUnsetFlags(t *testing.T) {
	withFlagDefaults(t)
	cmd := newRunCmdForTest()

	cfg := blueprint.Config{
		Frontend: "tui",
		Debug:    true,
		Log:      "/tmp/x.log",
		Web:      blueprint.WebConfig{Enabled: true, Port: 9000, Hostname: "https://x"},
		Store:    blueprint.StoreConfig{Type: "postgres", DSN: "postgres://x"},
	}
	applyBlueprintConfig(cmd, &cfg)

	if !debug {
		t.Error("debug should be true from config")
	}
	if logFile != "/tmp/x.log" {
		t.Errorf("logFile = %q", logFile)
	}
	if !useTUI {
		t.Error("useTUI should be true from config (frontend: tui)")
	}
	if !useWeb {
		t.Error("useWeb should be true from config")
	}
	if webPort != 9000 {
		t.Errorf("webPort = %d", webPort)
	}
	if webHostname != "https://x" {
		t.Errorf("webHostname = %q", webHostname)
	}
	if dbDSN != "postgres://x" {
		t.Errorf("dbDSN = %q", dbDSN)
	}
}

func TestApplyBlueprintConfig_FlagWinsOverConfig(t *testing.T) {
	withFlagDefaults(t)
	cmd := newRunCmdForTest()

	// Simulate `ofc run --web=false --port=7777` — flags explicitly set.
	if err := cmd.ParseFlags([]string{"--web=false", "--port=7777"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg := blueprint.Config{
		Web: blueprint.WebConfig{Enabled: true, Port: 9000},
	}
	applyBlueprintConfig(cmd, &cfg)

	if useWeb {
		t.Error("explicit --web=false should beat config web.enabled=true")
	}
	if webPort != 7777 {
		t.Errorf("explicit --port=7777 should beat config web.port=9000, got %d", webPort)
	}
}

func TestApplyBlueprintConfig_JSONFrontend(t *testing.T) {
	withFlagDefaults(t)
	cmd := newRunCmdForTest()
	cfg := blueprint.Config{Frontend: "json"}
	applyBlueprintConfig(cmd, &cfg)
	if !useJSON {
		t.Error("useJSON should be set from config frontend: json")
	}
}

func TestApplyBlueprintConfig_PostgresWithoutDSNStaysUnset(t *testing.T) {
	withFlagDefaults(t)
	cmd := newRunCmdForTest()
	cfg := blueprint.Config{Store: blueprint.StoreConfig{Type: "postgres"}}
	applyBlueprintConfig(cmd, &cfg)
	if dbDSN != "" {
		t.Errorf("dbDSN should stay empty when type=postgres but DSN missing, got %q", dbDSN)
	}
}
