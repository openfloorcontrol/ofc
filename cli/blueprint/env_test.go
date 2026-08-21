package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandString_Syntax(t *testing.T) {
	t.Setenv("OFC_SET", "value")
	t.Setenv("OFC_EMPTY", "")

	tests := []struct {
		name    string
		in      string
		want    string
		missing []string
	}{
		{"no reference", "plain text", "plain text", nil},

		// Only ${VAR} is a reference — a bare '$' is data.
		{"literal dollar", "cost: $5", "cost: $5", nil},
		{"bare dollar name", "$OFC_SET", "$OFC_SET", nil},
		{"unterminated", "${OFC_SET", "${OFC_SET", nil},
		{"set", "${OFC_SET}", "value", nil},
		{"embedded", "Bearer ${OFC_SET}!", "Bearer value!", nil},
		{"two refs", "${OFC_SET}/${OFC_SET}", "value/value", nil},

		// Set-but-empty is a deliberate choice, not a missing variable.
		{"empty is not missing", "${OFC_EMPTY}", "", nil},

		// Unset with no default is the error case.
		{"unset", "${OFC_UNSET}", "", []string{"OFC_UNSET"}},

		// ${VAR-def}: default applies only when unset.
		{"dash unset", "${OFC_UNSET-fallback}", "fallback", nil},
		{"dash set", "${OFC_SET-fallback}", "value", nil},
		{"dash empty keeps empty", "${OFC_EMPTY-fallback}", "", nil},

		// ${VAR:-def}: default applies when unset or empty.
		{"colon-dash unset", "${OFC_UNSET:-fallback}", "fallback", nil},
		{"colon-dash set", "${OFC_SET:-fallback}", "value", nil},
		{"colon-dash empty", "${OFC_EMPTY:-fallback}", "fallback", nil},

		{"empty default", "${OFC_UNSET:-}", "", nil},
		{"default containing dash", "${OFC_UNSET:-a-b-c}", "a-b-c", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var missing []missingVar
			got := expandString(tt.in, "test", &missing)
			if got != tt.want {
				t.Errorf("expandString(%q) = %q, want %q", tt.in, got, tt.want)
			}
			var names []string
			for _, m := range missing {
				names = append(names, m.Name)
			}
			if strings.Join(names, ",") != strings.Join(tt.missing, ",") {
				t.Errorf("missing = %v, want %v", names, tt.missing)
			}
		})
	}
}

// The walk must reach every string in the blueprint, not just the fields
// somebody remembered to wire up.
func TestExpandBlueprintEnv_ReachesEveryField(t *testing.T) {
	t.Setenv("OFC_TEST_KEY", "sk-123")
	t.Setenv("OFC_TEST_DSN", "postgres://db")
	t.Setenv("OFC_TEST_TOKEN", "tok")
	t.Setenv("OFC_TEST_BIN", "/usr/bin/agent")

	bp := &Blueprint{
		Defaults: Defaults{APIKey: "${OFC_TEST_KEY}"},
		Config:   Config{Store: StoreConfig{DSN: "${OFC_TEST_DSN}"}},
		Agents: []Agent{{
			ID:      "@claude",
			Command: "${OFC_TEST_BIN}",
			Args:    []string{"--key", "${OFC_TEST_KEY}"},
			Env:     map[string]string{"ANTHROPIC_API_KEY": "${OFC_TEST_KEY}"},
		}},
		Furniture: []FurnitureDef{{
			Name:    "remote",
			Headers: map[string]string{"Authorization": "Bearer ${OFC_TEST_TOKEN}"},
		}},
	}

	if err := expandBlueprintEnv(bp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := map[string]string{
		"defaults.api_key": bp.Defaults.APIKey,
		"config.store.dsn": bp.Config.Store.DSN,
		"agent command":    bp.Agents[0].Command,
		"agent args":       bp.Agents[0].Args[1],
		"agent env":        bp.Agents[0].Env["ANTHROPIC_API_KEY"],
		"furniture header": bp.Furniture[0].Headers["Authorization"],
	}
	want := map[string]string{
		"defaults.api_key": "sk-123",
		"config.store.dsn": "postgres://db",
		"agent command":    "/usr/bin/agent",
		"agent args":       "sk-123",
		"agent env":        "sk-123",
		"furniture header": "Bearer tok",
	}
	for k, got := range checks {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}
}

// A '$' inside an expanded value is data, not a further reference. Secrets do
// contain '$', and a second expansion pass would silently truncate them.
func TestExpandBlueprintEnv_DollarInValueSurvives(t *testing.T) {
	t.Setenv("OFC_TRICKY_KEY", "sk-ab$cd-ef")

	bp := &Blueprint{Defaults: Defaults{APIKey: "${OFC_TRICKY_KEY}"}}
	if err := expandBlueprintEnv(bp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.Defaults.APIKey != "sk-ab$cd-ef" {
		t.Errorf("api_key = %q, want the value intact", bp.Defaults.APIKey)
	}
}

// Prompts are content and opt out — a Python f-string must survive intact.
// Prompts are blueprint.yaml text like any other field.
func TestExpandBlueprintEnv_ReachesPrompts(t *testing.T) {
	t.Setenv("OFC_SHOP", "Tokyo")

	bp := &Blueprint{Agents: []Agent{{ID: "@data", Prompt: "You work in ${OFC_SHOP}."}}}
	if err := expandBlueprintEnv(bp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.Agents[0].Prompt != "You work in Tokyo." {
		t.Errorf("prompt = %q, want the variable expanded", bp.Agents[0].Prompt)
	}
}

// All missing variables are reported at once, with their locations.
func TestExpandBlueprintEnv_ReportsAllMissing(t *testing.T) {
	bp := &Blueprint{
		Defaults: Defaults{APIKey: "${OFC_MISSING_ONE}"},
		Agents: []Agent{{
			ID:  "@a",
			Env: map[string]string{"TOKEN": "${OFC_MISSING_TWO}"},
		}},
	}

	err := expandBlueprintEnv(bp)
	if err == nil {
		t.Fatal("expected an error for unset variables")
	}

	var envErr *MissingEnvError
	if !asMissingEnv(err, &envErr) {
		t.Fatalf("expected *MissingEnvError, got %T", err)
	}
	if len(envErr.Missing) != 2 {
		t.Fatalf("expected 2 missing vars, got %d: %v", len(envErr.Missing), envErr.Missing)
	}

	msg := err.Error()
	for _, want := range []string{"OFC_MISSING_ONE", "OFC_MISSING_TWO", "defaults.api_key", "agents[0].env[TOKEN]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q:\n%s", want, msg)
		}
	}
}

func TestLoad_MissingEnvIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.yaml")
	yaml := `name: test
defaults:
  api_key: ${OFC_DEFINITELY_NOT_SET}
agents:
  - id: "@a"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to fail on an unset variable")
	} else if !strings.Contains(err.Error(), "OFC_DEFINITELY_NOT_SET") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

func TestLoad_EnvDefaultKeepsLoadWorking(t *testing.T) {
	// OFC_ENDPOINT is commonly set in dev shells — clear it so the ${VAR:-…}
	// default is what gets tested, not whatever the developer exported.
	t.Setenv("OFC_ENDPOINT", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.yaml")
	yaml := `name: test
defaults:
  endpoint: ${OFC_ENDPOINT:-http://localhost:11434/v1}
agents:
  - id: "@a"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	bp, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.Defaults.Endpoint != "http://localhost:11434/v1" {
		t.Errorf("endpoint = %q, want the default", bp.Defaults.Endpoint)
	}
}

func asMissingEnv(err error, target **MissingEnvError) bool {
	e, ok := err.(*MissingEnvError)
	if ok {
		*target = e
	}
	return ok
}
