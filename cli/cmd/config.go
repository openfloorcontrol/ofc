package cmd

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Every flag --foo-bar of every command can also be set by the
// environment variable OFC_FOO_BAR. A config file (--config, or
// $OFC_CONFIG) is an env file: its variables are added to the
// environment before flags are resolved, and a variable the environment
// already has keeps its value. So a setting comes from, in order:
//
//	the flag when passed > the environment > the config file > the flag's default
//
// Variables in the config file that are not OFC_* settings are ordinary
// environment variables: blueprints read them with ${VAR}, and
// subprocesses (MCP servers, ACP agents) inherit them.

var configFile string

const configFlag = "config"

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, configFlag, "", "Env file of settings (OFC_PORT=8080, …) and variables for the blueprint")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := applyEnvironment(cmd); err != nil {
			cmd.SilenceUsage = true
			return err
		}
		return nil
	}
}

// envName is the environment variable for a flag: --oauth-dir is
// OFC_OAUTH_DIR.
func envName(flag string) string {
	return "OFC_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// cobraFlags are flags cobra adds to every command; they have no
// environment variable.
var cobraFlags = map[string]bool{"help": true, "version": true}

// applyEnvironment loads the config file into the environment and then
// sets every flag of cmd that was not passed from its variable.
func applyEnvironment(cmd *cobra.Command) error {
	path := configFile
	if !cmd.Flags().Changed(configFlag) {
		path = os.Getenv(envName(configFlag))
	}
	if path != "" {
		if err := loadConfigFile(path, settingNames(cmd.Root())); err != nil {
			return err
		}
	}

	var err error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if err != nil || f.Changed || cobraFlags[f.Name] || f.Name == configFlag {
			return
		}
		name := envName(f.Name)
		val, ok := os.LookupEnv(name)
		if !ok {
			return
		}
		if setErr := f.Value.Set(val); setErr != nil {
			err = fmt.Errorf("%s=%q: %v", name, val, setErr)
		}
	})
	return err
}

// settingNames returns the variable of every flag of every command.
func settingNames(root *cobra.Command) map[string]bool {
	names := map[string]bool{}
	add := func(f *pflag.Flag) {
		if !cobraFlags[f.Name] {
			names[envName(f.Name)] = true
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.LocalFlags().VisitAll(add)
		c.PersistentFlags().VisitAll(add)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return names
}

// loadConfigFile adds the variables of the env file at path to the
// environment. A variable the environment already has keeps its value;
// when the file's value differs, a warning names it. An OFC_* variable
// must be one of settings, and the file cannot name another config file.
func loadConfigFile(path string, settings map[string]bool) error {
	vars, err := readEnvFile(path)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if v.key == envName(configFlag) {
			return fmt.Errorf("%s:%d: %s cannot be set in a config file", path, v.line, v.key)
		}
		if strings.HasPrefix(v.key, "OFC_") && !settings[v.key] {
			return fmt.Errorf("%s:%d: %s is not an ofc setting", path, v.line, v.key)
		}
	}
	for _, v := range vars {
		if cur, set := os.LookupEnv(v.key); set {
			if cur != v.value {
				fmt.Fprintf(os.Stderr, "[warning] %s from %s is not used: the environment sets it\n", v.key, path)
			}
			continue
		}
		os.Setenv(v.key, v.value)
	}
	return nil
}

type envVar struct {
	key, value string
	line       int
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile parses an env file: one KEY=VALUE per line, where VALUE is
// everything after the first '=', taken literally. Blank lines and lines
// starting with '#' are skipped. A key may appear once.
func readEnvFile(path string) ([]envVar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	defer f.Close()

	var vars []envVar
	seen := map[string]int{}
	lines := bufio.NewScanner(f)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSuffix(lines.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKey.MatchString(key) {
			return nil, fmt.Errorf("%s:%d: want KEY=VALUE, got %q", path, n, line)
		}
		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s:%d: %s is already set on line %d", path, n, key, first)
		}
		seen[key] = n
		vars = append(vars, envVar{key: key, value: value, line: n})
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	return vars, nil
}
