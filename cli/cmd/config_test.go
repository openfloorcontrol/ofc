package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// unsetenv makes name unset for the test; the previous value is
// restored afterwards.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	os.Unsetenv(name)
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsCmd is a command tree with --config and a "serve" command
// with --port and --name, resolved by applyEnvironment as in ofc.
type settingsCmd struct {
	root *cobra.Command
	port int
	name string
}

func newSettingsCmd() *settingsCmd {
	s := &settingsCmd{root: &cobra.Command{Use: "ofc"}}
	s.root.PersistentFlags().StringVar(&configFile, configFlag, "", "")
	s.root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return applyEnvironment(cmd)
	}
	serve := &cobra.Command{Use: "serve", RunE: func(*cobra.Command, []string) error { return nil }}
	serve.Flags().IntVar(&s.port, "port", 8080, "")
	serve.Flags().StringVar(&s.name, "name", "default", "")
	s.root.AddCommand(serve)
	s.root.SilenceErrors = true
	s.root.SilenceUsage = true
	return s
}

func (s *settingsCmd) run(args ...string) error {
	s.root.SetArgs(append([]string{"serve"}, args...))
	return s.root.Execute()
}

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"port":                "OFC_PORT",
		"oauth-callback-port": "OFC_OAUTH_CALLBACK_PORT",
		"acp-idle-timeout":    "OFC_ACP_IDLE_TIMEOUT",
	}
	for flag, want := range cases {
		if got := envName(flag); got != want {
			t.Errorf("envName(%q) = %q, want %q", flag, got, want)
		}
	}
}

func TestSettingsPrecedence(t *testing.T) {
	file := writeFile(t, "OFC_PORT=7000\nOFC_NAME=from-file\n")
	cases := []struct {
		label string
		env   map[string]string
		args  []string
		port  int
		name  string
	}{
		{"default", nil, nil, 8080, "default"},
		{"environment", map[string]string{"OFC_PORT": "9000"}, nil, 9000, "default"},
		{"file", nil, []string{"--config", file}, 7000, "from-file"},
		{"environment over file", map[string]string{"OFC_PORT": "9000"}, []string{"--config", file}, 9000, "from-file"},
		{"flag over environment and file", map[string]string{"OFC_PORT": "9000"}, []string{"--config", file, "--port", "1"}, 1, "from-file"},
		{"file named by OFC_CONFIG", map[string]string{"OFC_CONFIG": file}, nil, 7000, "from-file"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			for _, name := range []string{"OFC_PORT", "OFC_NAME", "OFC_CONFIG"} {
				unsetenv(t, name)
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			s := newSettingsCmd()
			if err := s.run(c.args...); err != nil {
				t.Fatal(err)
			}
			if s.port != c.port || s.name != c.name {
				t.Errorf("port, name = %d, %q; want %d, %q", s.port, s.name, c.port, c.name)
			}
		})
	}
}

func TestConfigFileExportsOtherVariables(t *testing.T) {
	unsetenv(t, "OFC_TEST_DEPLOY")
	unsetenv(t, "DEPLOY_ENVIRONMENT")
	t.Setenv("OFC_TEST_KEEP", "env")
	file := writeFile(t, "DEPLOY_ENVIRONMENT=prod\nOFC_TEST_KEEP=file\n")

	settings := map[string]bool{"OFC_TEST_KEEP": true}
	if err := loadConfigFile(file, settings); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DEPLOY_ENVIRONMENT"); got != "prod" {
		t.Errorf("DEPLOY_ENVIRONMENT = %q, want prod", got)
	}
	if got := os.Getenv("OFC_TEST_KEEP"); got != "env" {
		t.Errorf("OFC_TEST_KEEP = %q, want the environment's value", got)
	}
}

func TestConfigFileErrors(t *testing.T) {
	unsetenv(t, "OFC_PORT")
	unsetenv(t, "OFC_CONFIG")
	cases := map[string]string{
		"unknown setting":    "OFC_PROT=9000\n",
		"nested config":      "OFC_CONFIG=other.env\n",
		"not KEY=VALUE":      "OFC_PORT 9000\n",
		"bad key":            "OFC-PORT=9000\n",
		"duplicate key":      "OFC_PORT=1\nOFC_PORT=2\n",
		"value not a number": "OFC_PORT=eighty\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			s := newSettingsCmd()
			if err := s.run("--config", writeFile(t, body)); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestBadEnvironmentValueNamesVariable(t *testing.T) {
	t.Setenv("OFC_PORT", "eighty")
	s := newSettingsCmd()
	err := s.run()
	if err == nil || !strings.Contains(err.Error(), "OFC_PORT") {
		t.Errorf("want an error naming OFC_PORT, got %v", err)
	}
}

func TestReadEnvFile(t *testing.T) {
	file := writeFile(t, "# deployment\n\nA=1\r\nB= spaced value \nC=x=y\nD=\n")
	vars, err := readEnvFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := []envVar{
		{key: "A", value: "1", line: 3},
		{key: "B", value: " spaced value ", line: 4},
		{key: "C", value: "x=y", line: 5},
		{key: "D", value: "", line: 6},
	}
	if len(vars) != len(want) {
		t.Fatalf("got %d vars, want %d: %+v", len(vars), len(want), vars)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Errorf("var %d = %+v, want %+v", i, vars[i], want[i])
		}
	}
}

func TestEveryOfcFlagHasASetting(t *testing.T) {
	names := settingNames(rootCmd)
	for _, want := range []string{
		"OFC_CONFIG", "OFC_BLUEPRINT", "OFC_PORT", "OFC_BIND", "OFC_TOKEN", "OFC_DB",
		"OFC_SESSIONS_DIR", "OFC_OAUTH_DIR", "OFC_OAUTH_CALLBACK", "OFC_OAUTH_CALLBACK_PORT",
		"OFC_ACP_IDLE_TIMEOUT", "OFC_API_KEY", "OFC_REMOTE",
	} {
		if !names[want] {
			t.Errorf("no setting %s", want)
		}
	}
	if names["OFC_HELP"] {
		t.Error("OFC_HELP should not be a setting")
	}
}
