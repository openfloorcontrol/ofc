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

// A model reference names a blueprint model first, else an mf address.
func TestModelReferences(t *testing.T) {
	withMFRegistry(t)
	t.Setenv("OFC_TEST_LOCAL_KEY", "sk-local")
	bp, err := loadBlueprint(t, `name: t
models:
  local:
    endpoint: http://localhost:11434/v1
    model: qwen3:8b
    api_key: ${OFC_TEST_LOCAL_KEY}
  fast:
    endpoint: http://localhost:11434/v1
    model: shadows-mf-fast
defaults:
  model: "@coding"
agents:
  - id: "@default"
  - id: "@local"
    model: local
  - id: "@shadowed"
    model: fast
  - id: "@alias"
    model: smart
  - id: "@pair"
    model: gw/some-model
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][4]string{
		"@default":  {"https://gw.example.com/v1", "groq/qwen-fast", "sk-gw", "@coding (mf) → gw/groq/qwen-fast"},
		"@local":    {"http://localhost:11434/v1", "qwen3:8b", "sk-local", "local (blueprint) → qwen3:8b"},
		"@shadowed": {"http://localhost:11434/v1", "shadows-mf-fast", "", "fast (blueprint) → shadows-mf-fast"},
		"@alias":    {"https://gw.example.com/v1", "anthropic/claude-smart", "sk-gw", "smart (mf) → gw/anthropic/claude-smart"},
		"@pair":     {"https://gw.example.com/v1", "some-model", "sk-gw", "gw/some-model (mf) → gw/some-model"},
	}
	for _, a := range bp.Agents {
		got := [4]string{a.Endpoint, a.Model, a.APIKey, a.ModelSource}
		if got != want[a.ID] {
			t.Errorf("%s: endpoint, model, key, source = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

func TestModelErrors(t *testing.T) {
	withMFRegistry(t)
	cases := map[string]string{
		"unknown reference":      "agents:\n  - id: \"@a\"\n    model: \"@nope\"\n",
		"blueprint model w/o id": "models:\n  x:\n    endpoint: http://x\nagents:\n  - id: \"@a\"\n    model: x\n",
		"ACP agent with model":   "agents:\n  - id: \"@a\"\n    type: acp\n    command: x\n    model: fast\n",
		"endpoint on an agent":   "agents:\n  - id: \"@a\"\n    endpoint: http://x\n",
	}
	for label, body := range cases {
		if _, err := loadBlueprint(t, "name: t\n"+body); err == nil {
			t.Errorf("%s: want an error", label)
		} else if label == "unknown reference" && !strings.Contains(err.Error(), "@nope") {
			t.Errorf("%s: error %q should name the reference", label, err)
		}
	}
}

func TestMFKeyFromEnvironment(t *testing.T) {
	withMFRegistry(t)
	t.Setenv("MF_KEY_GW", "sk-env")
	bp, err := loadBlueprint(t, "name: t\nagents:\n  - id: \"@a\"\n    model: fast\n")
	if err != nil {
		t.Fatal(err)
	}
	if bp.Agents[0].APIKey != "sk-env" {
		t.Errorf("api key = %q, want MF_KEY_GW's value", bp.Agents[0].APIKey)
	}
}

// Blueprint models don't touch mf: a broken registry only fails a
// blueprint that needs it.
func TestBlueprintModelsDoNotReadRegistry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.toml"), []byte("this is [not toml"), 0o644)
	t.Setenv("MF_CONFIG_DIR", dir)
	if _, err := loadBlueprint(t, "name: t\nagents:\n  - id: \"@a\"\n    model: fast\n"); err == nil {
		t.Fatal("the broken registry should fail a blueprint that uses mf")
	}
	body := "name: t\nmodels:\n  local:\n    endpoint: http://x\n    model: m\nagents:\n  - id: \"@a\"\n    model: local\n"
	if _, err := loadBlueprint(t, body); err != nil {
		t.Errorf("a blueprint with only its own models failed to load: %v", err)
	}
}
