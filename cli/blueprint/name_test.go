package blueprint

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadParsesACPIdleTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blueprint.yaml")
	os.WriteFile(path, []byte("name: t\nconfig:\n  acp_idle_timeout: 15m\n"), 0o644)
	bp, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if bp.Config.ACPIdleTimeout != 15*time.Minute {
		t.Errorf("ACPIdleTimeout = %v, want 15m", bp.Config.ACPIdleTimeout)
	}
}

func TestLoadValidatesOAuth(t *testing.T) {
	const head = "name: t\nfurniture:\n  - name: kb\n    type: mcp\n"
	cases := map[string]bool{
		"    url: https://kb\n    oauth: {}\n": true,
		"    url: https://kb\n    oauth: {grant: client_credentials, client_id: a, client_secret: b}\n": true,
		"    command: kbmcp\n    oauth: {}\n":                                         false, // stdio
		"    url: https://kb\n    oauth: {grant: client_credentials, client_id: a}\n": false, // no secret
		"    url: https://kb\n    oauth: {grant: implicit}\n":                         false,
	}
	for tail, ok := range cases {
		path := filepath.Join(t.TempDir(), "blueprint.yaml")
		os.WriteFile(path, []byte(head+tail), 0o644)
		_, err := Load(path)
		if ok && err != nil {
			t.Errorf("%q: unexpected error %v", tail, err)
		}
		if !ok && err == nil {
			t.Errorf("%q: expected an error", tail)
		}
	}
}

func TestLoadRequiresURLSafeName(t *testing.T) {
	cases := map[string]bool{
		"name: taskboard-demo\n": true,
		"name: v1.2_ok\n":        true,
		"description: none\n":    false,
		"name: My Floor\n":       false,
		"name: a/b\n":            false,
	}
	for yaml, ok := range cases {
		path := filepath.Join(t.TempDir(), "blueprint.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if ok && err != nil {
			t.Errorf("%q: unexpected error %v", yaml, err)
		}
		if !ok && err == nil {
			t.Errorf("%q: expected an error", yaml)
		}
	}
}
