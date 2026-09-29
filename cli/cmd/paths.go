package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
