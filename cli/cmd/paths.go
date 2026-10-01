package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/sessionstore"
)

// defaultSessionsDir returns the directory where session JSONL files live
// by default. Resolution order:
//  1. $OFC_SESSIONS_DIR if set
//  2. $HOME/.ofc/sessions
//
// Once config files land (step 6), this will also honor `storage.dir` in
// ~/.ofc/config.yaml and ./ofc.yaml.
func defaultSessionsDir() (string, error) {
	if d := os.Getenv("OFC_SESSIONS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".ofc", "sessions"), nil
}

// defaultOAuthDir returns where OAuth tokens for furniture live:
// $OFC_OAUTH_DIR if set, else $HOME/.ofc/oauth.
func defaultOAuthDir() (string, error) {
	if d := os.Getenv("OFC_OAUTH_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".ofc", "oauth"), nil
}

// applyOAuthCallback sets the floor's consent callback from
// $OFC_OAUTH_CALLBACK (the URL registered with authorization servers) and
// $OFC_OAUTH_CALLBACK_PORT (the 127.0.0.1 port ofc listens on, behind a
// proxy serving that URL).
func applyOAuthCallback(f *floor.Floor) error {
	f.OAuthCallbackURL = os.Getenv("OFC_OAUTH_CALLBACK")
	if p := os.Getenv("OFC_OAUTH_CALLBACK_PORT"); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("OFC_OAUTH_CALLBACK_PORT=%q: want a port number", p)
		}
		if f.OAuthCallbackURL == "" {
			return fmt.Errorf("OFC_OAUTH_CALLBACK_PORT needs OFC_OAUTH_CALLBACK, the URL that reaches it")
		}
		f.OAuthCallbackPort = port
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

// openSessionStore opens the configured backend: Postgres if --db or
// $OFC_DATABASE_URL is set, otherwise the JSONL sessions directory.
// Returns a short label for the backend, for display.
func openSessionStore() (sessionStore, string, error) {
	dsn := dbDSN
	if dsn == "" {
		dsn = os.Getenv("OFC_DATABASE_URL")
	}
	if dsn != "" {
		pg, err := sessionstore.OpenPostgres(context.Background(), dsn)
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
