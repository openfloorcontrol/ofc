package cmd

import (
	"fmt"
	"os"
	"path/filepath"
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

// ensureSessionsDir resolves defaultSessionsDir and creates it if missing.
func ensureSessionsDir() (string, error) {
	dir, err := defaultSessionsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create sessions dir %s: %w", dir, err)
	}
	return dir, nil
}

// sessionPath returns the file path for a session with the given UUID
// in the default sessions directory.
func sessionPath(sessionID string) (string, error) {
	dir, err := ensureSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".jsonl"), nil
}
