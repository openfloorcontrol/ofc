package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
	"github.com/spf13/pflag"
)

var (
	sessionsDir       string
	oauthDir          string
	oauthCallback     string
	oauthCallbackPort int
)

// addBlueprintFlag registers -f/--blueprint, the blueprint file.
func addBlueprintFlag(fs *pflag.FlagSet, p *string) {
	fs.StringVarP(p, "blueprint", "f", "blueprint.yaml", "Blueprint file")
}

// addStoreFlags registers the flags that select the session store.
func addStoreFlags(fs *pflag.FlagSet) {
	fs.StringVar(&dbDSN, "db", "", "Postgres DSN for session storage (default: JSONL files in --sessions-dir)")
	fs.StringVar(&sessionsDir, "sessions-dir", "", "Directory of JSONL session files (default: ~/.ofc/sessions)")
}

// addOAuthFlags registers the flags for OAuth furniture.
func addOAuthFlags(fs *pflag.FlagSet) {
	fs.StringVar(&oauthDir, "oauth-dir", "", "Directory of stored OAuth tokens (default: ~/.ofc/oauth)")
	fs.StringVar(&oauthCallback, "oauth-callback", "", "Consent callback URL registered with authorization servers (default: a free loopback port)")
	fs.IntVar(&oauthCallbackPort, "oauth-callback-port", 0, "127.0.0.1 port to listen on for --oauth-callback, behind a proxy serving that URL")
}

// defaultSessionsDir returns the directory where session JSONL files
// live: --sessions-dir if set, else $HOME/.ofc/sessions.
func defaultSessionsDir() (string, error) {
	if sessionsDir != "" {
		return sessionsDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".ofc", "sessions"), nil
}

// defaultOAuthDir returns where OAuth tokens for furniture live:
// --oauth-dir if set, else $HOME/.ofc/oauth.
func defaultOAuthDir() (string, error) {
	if oauthDir != "" {
		return oauthDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".ofc", "oauth"), nil
}

// applyOAuthCallback sets the floor's consent callback from
// --oauth-callback (the URL registered with authorization servers) and
// --oauth-callback-port (the 127.0.0.1 port ofc listens on, behind a
// proxy serving that URL).
func applyOAuthCallback(f *floor.Floor) error {
	f.OAuthCallbackURL = oauthCallback
	if oauthCallbackPort != 0 {
		if oauthCallbackPort < 0 || oauthCallbackPort > 65535 {
			return fmt.Errorf("--oauth-callback-port %d: want a port number", oauthCallbackPort)
		}
		if f.OAuthCallbackURL == "" {
			return fmt.Errorf("--oauth-callback-port needs --oauth-callback, the URL that reaches it")
		}
		f.OAuthCallbackPort = oauthCallbackPort
	}
	return nil
}

// interactive reports whether a person is at the terminal: stdin is a
// terminal and the output is not --json. Only then may ofc ask for OAuth
// consent.
func interactive() bool {
	if useJSON {
		return false
	}
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// sessionStore is a floor.SessionStore that holds resources (files, a
// DB pool) the caller must release.
type sessionStore interface {
	floor.SessionStore
	Close() error
}

// openSessionStore opens the configured backend: Postgres if --db is
// set, otherwise the JSONL sessions directory. Returns a short label for
// the backend, for display.
func openSessionStore() (sessionStore, string, error) {
	if dbDSN != "" {
		pg, err := sessionstore.OpenPostgres(context.Background(), dbDSN)
		if err != nil {
			return nil, "", fmt.Errorf("session store: %w", err)
		}
		return pg, "postgres", nil
	}
	dir, err := defaultSessionsDir()
	if err != nil {
		return nil, "", err
	}
	jl, err := sessionstore.NewJSONL(dir)
	if err != nil {
		return nil, "", fmt.Errorf("session store: %w", err)
	}
	return jl, "jsonl", nil
}
