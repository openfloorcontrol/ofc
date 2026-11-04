package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// withStoreFlags sets the session-store flags for the test.
func withStoreFlags(t *testing.T, dir, dsn string) {
	t.Helper()
	prevDir, prevDSN := sessionsDir, dbDSN
	sessionsDir, dbDSN = dir, dsn
	t.Cleanup(func() { sessionsDir, dbDSN = prevDir, prevDSN })
}

func TestDefaultSessionsDirFlag(t *testing.T) {
	withStoreFlags(t, "/tmp/custom-sessions", "")
	got, err := defaultSessionsDir()
	if err != nil {
		t.Fatalf("defaultSessionsDir: %v", err)
	}
	if got != "/tmp/custom-sessions" {
		t.Errorf("expected /tmp/custom-sessions, got %q", got)
	}
}

func TestDefaultSessionsDirFallback(t *testing.T) {
	withStoreFlags(t, "", "")
	got, err := defaultSessionsDir()
	if err != nil {
		t.Fatalf("defaultSessionsDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".ofc", "sessions")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestOpenSessionStoreDefaultsToJSONLDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	withStoreFlags(t, dir, "")

	store, label, err := openSessionStore()
	if err != nil {
		t.Fatalf("openSessionStore: %v", err)
	}
	defer store.Close()
	if label != "jsonl" {
		t.Errorf("expected jsonl backend, got %q", label)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("sessions dir not created: %v", err)
	}
}
