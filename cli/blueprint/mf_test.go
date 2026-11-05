package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withMFRegistry points MF_CONFIG_DIR at a registry with endpoint gw and
// models fast (tag coding) and smart; gw's key is sk-gw.
func withMFRegistry(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.toml"), []byte(`
[[endpoint]]
name = 'gw'
url  = 'https://gw.example.com/v1'

[[model]]
name     = 'fast'
endpoint = 'gw'
model    = 'groq/qwen-fast'
tags     = ['coding']

[[model]]
name     = 'smart'
endpoint = 'gw'
model    = 'anthropic/claude-smart'
`), 0o644)
	os.WriteFile(filepath.Join(dir, "models.secrets"), []byte(`
[[secret]]
endpoint = 'gw'
key      = 'sk-gw'
`), 0o600)
	t.Setenv("MF_CONFIG_DIR", dir)
	t.Setenv("MF_KEY_GW", "")
	os.Unsetenv("MF_KEY_GW")
}

func loadBlueprint(t *testing.T, body string) (*Blueprint, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blueprint.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestMFSetsAgentModel(t *testing.T) {
	withMFRegistry(t)
	bp, err := loadBlueprint(t, `name: t
defaults:
  mf: "@coding"
agents:
  - id: "@default"
  - id: "@alias"
    mf: smart
  - id: "@pair"
    mf: gw/some-model
  - id: "@override"
    model: other-model
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{
		"@default":  {"https://gw.example.com/v1", "groq/qwen-fast", "sk-gw"},
		"@alias":    {"https://gw.example.com/v1", "anthropic/claude-smart", "sk-gw"},
		"@pair":     {"https://gw.example.com/v1", "some-model", "sk-gw"},
		"@override": {"https://gw.example.com/v1", "other-model", "sk-gw"},
	}
	for _, a := range bp.Agents {
		got := [3]string{a.Endpoint, a.Model, a.APIKey}
		if got != want[a.ID] {
			t.Errorf("%s: endpoint, model, key = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

func TestMFErrors(t *testing.T) {
	withMFRegistry(t)
	cases := map[string]string{
		"unknown tag":            "agents:\n  - id: \"@a\"\n    mf: \"@nope\"\n",
		"agent mf with model":    "agents:\n  - id: \"@a\"\n    mf: fast\n    model: x\n",
		"defaults mf with model": "defaults:\n  mf: fast\n  endpoint: http://x\nagents:\n  - id: \"@a\"\n",
		"ACP agent":              "agents:\n  - id: \"@a\"\n    type: acp\n    command: x\n    mf: fast\n",
	}
	for label, body := range cases {
		if _, err := loadBlueprint(t, "name: t\n"+body); err == nil {
			t.Errorf("%s: want an error", label)
		} else if label == "unknown tag" && !strings.Contains(err.Error(), "@nope") {
			t.Errorf("%s: error %q should name the address", label, err)
		}
	}
}

func TestMFKeyFromEnvironment(t *testing.T) {
	withMFRegistry(t)
	t.Setenv("MF_KEY_GW", "sk-env")
	bp, err := loadBlueprint(t, "name: t\nagents:\n  - id: \"@a\"\n    mf: fast\n")
	if err != nil {
		t.Fatal(err)
	}
	if bp.Agents[0].APIKey != "sk-env" {
		t.Errorf("api key = %q, want MF_KEY_GW's value", bp.Agents[0].APIKey)
	}
}

func TestBlueprintWithoutMFDoesNotReadRegistry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.toml"), []byte("this is [not toml"), 0o644)
	t.Setenv("MF_CONFIG_DIR", dir)
	if _, err := loadBlueprint(t, "name: t\nagents:\n  - id: \"@a\"\n    mf: fast\n"); err == nil {
		t.Fatal("the broken registry should fail a blueprint that uses mf")
	}
	if _, err := loadBlueprint(t, "name: t\nagents:\n  - id: \"@a\"\n    endpoint: http://x\n    model: m\n"); err != nil {
		t.Errorf("a blueprint without mf failed to load: %v", err)
	}
}
