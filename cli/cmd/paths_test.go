package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSessionsDirEnvOverride(t *testing.T) {
	t.Setenv("OFC_SESSIONS_DIR", "/tmp/custom-sessions")
	got, err := defaultSessionsDir()
	if err != nil {
		t.Fatalf("defaultSessionsDir: %v", err)
	}
	if got != "/tmp/custom-sessions" {
		t.Errorf("expected /tmp/custom-sessions, got %q", got)
	}
}

func TestDefaultSessionsDirFallback(t *testing.T) {
	t.Setenv("OFC_SESSIONS_DIR", "")
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
	t.Setenv("OFC_SESSIONS_DIR", dir)
	t.Setenv("OFC_DATABASE_URL", "")

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
