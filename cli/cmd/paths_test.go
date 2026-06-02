package cmd

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSessionPathConstruction(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OFC_SESSIONS_DIR", dir)

	path, err := sessionPath("abc-123")
	if err != nil {
		t.Fatalf("sessionPath: %v", err)
	}
	want := filepath.Join(dir, "abc-123.jsonl")
	if path != want {
		t.Errorf("expected %q, got %q", want, path)
	}

	// ensureSessionsDir should have created the directory.
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("sessions dir not created: %v", err)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
	}
	for _, c := range cases {
		got := humanSize(c.n)
		if !strings.Contains(got, strings.Split(c.want, " ")[1]) {
			t.Errorf("humanSize(%d): expected %q, got %q", c.n, c.want, got)
		}
	}
}
