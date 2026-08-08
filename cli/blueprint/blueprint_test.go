package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render builds an agent the way Load would, minus the cached template, so
// RenderPrompt takes its on-demand parse path.
func render(prompt, dir string) (string, error) {
	a := Agent{Prompt: prompt, dir: dir}
	return a.RenderPrompt()
}

func TestRenderPrompt_NoMarker(t *testing.T) {
	// No <% marker → returns unchanged, no template parsing
	in := "Hello {{ this }} should pass through unchanged"
	out, err := render(in, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != in {
		t.Errorf("expected unchanged, got: %q", out)
	}
}

func TestRenderPrompt_Readfile(t *testing.T) {
	dir := t.TempDir()
	contentPath := filepath.Join(dir, "catalog.md")
	if err := os.WriteFile(contentPath, []byte("- Product A\n- Product B\n"), 0644); err != nil {
		t.Fatal(err)
	}

	prompt := "Available:\n<% readfile \"catalog.md\" %>"
	out, err := render(prompt, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "- Product A") {
		t.Errorf("expected catalog content in output, got: %q", out)
	}
}

func TestRenderPrompt_ReadfileMissing(t *testing.T) {
	prompt := `<% readfile "nonexistent.md" %>`
	_, err := render(prompt, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestRenderPrompt_Env(t *testing.T) {
	t.Setenv("OFC_TEST_VAR", "hello")
	prompt := `<% env "OFC_TEST_VAR" %>`
	out, err := render(prompt, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("expected 'hello', got: %q", out)
	}
}

func TestRenderPrompt_ParseError(t *testing.T) {
	// Unclosed action
	prompt := `<% readfile "x" `
	_, err := render(prompt, "")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// Each render reads the world again, so a file edited mid-session shows up on
// the next turn.
func TestRenderPrompt_ReReadsPerRender(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.md")
	if err := os.WriteFile(path, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}

	a := Agent{Prompt: `<% readfile "catalog.md" %>`, dir: dir}

	first, err := a.RenderPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := a.RenderPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first != "first" || second != "second" {
		t.Errorf("renders = %q then %q, want the file re-read each time", first, second)
	}
}

// A template syntax error fails the load rather than the first turn.
func TestLoad_PromptTemplateSyntaxErrorFailsLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.yaml")
	yaml := "name: test\nagents:\n  - id: \"@a\"\n    prompt: '<% readfile \"x\" '\n"
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to reject an unparseable prompt template")
	} else if !strings.Contains(err.Error(), "prompt template") {
		t.Errorf("error should name the prompt template, got: %v", err)
	}
}

// Load leaves the template unrendered so each turn can render it afresh.
func TestLoad_KeepsPromptTemplateUnrendered(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catalog.md"), []byte("PRODUCTS"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "blueprint.yaml")
	yaml := "name: test\nagents:\n  - id: \"@a\"\n    prompt: 'Stock: <% readfile \"catalog.md\" %>'\n"
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	bp, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(bp.Agents[0].Prompt, "<% readfile") {
		t.Errorf("Prompt = %q, want the raw template", bp.Agents[0].Prompt)
	}

	out, err := bp.Agents[0].RenderPrompt()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "Stock: PRODUCTS" {
		t.Errorf("rendered = %q, want the catalog inlined", out)
	}
}

func TestLoad_DirIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	bpYAML := "name: test\nagents: []\n"
	bpPath := filepath.Join(dir, "blueprint.yaml")
	if err := os.WriteFile(bpPath, []byte(bpYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Pass a relative-looking path; Load should still resolve Dir absolutely.
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	bp, err := Load("blueprint.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(bp.Dir) {
		t.Errorf("expected absolute Dir, got: %q", bp.Dir)
	}
}

func TestLoad_ConfigSection(t *testing.T) {
	dir := t.TempDir()
	bpPath := filepath.Join(dir, "bp.yaml")
	os.Setenv("OFC_TEST_DSN", "postgres://example/x")
	defer os.Unsetenv("OFC_TEST_DSN")

	body := `name: example
config:
  frontend: tui
  debug: true
  log: /tmp/ofc.log
  web:
    enabled: true
    port: 9000
    hostname: https://ofc.example.com
  store:
    type: postgres
    dsn: ${OFC_TEST_DSN}
agents:
  - id: "@a"
`
	if err := os.WriteFile(bpPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write blueprint: %v", err)
	}

	bp, err := Load(bpPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := bp.Config
	if c.Frontend != "tui" {
		t.Errorf("Frontend = %q, want tui", c.Frontend)
	}
	if !c.Debug {
		t.Error("Debug should be true")
	}
	if c.Log != "/tmp/ofc.log" {
		t.Errorf("Log = %q", c.Log)
	}
	if !c.Web.Enabled {
		t.Error("Web.Enabled should be true")
	}
	if c.Web.Port != 9000 {
		t.Errorf("Web.Port = %d, want 9000", c.Web.Port)
	}
	if c.Web.Hostname != "https://ofc.example.com" {
		t.Errorf("Web.Hostname = %q", c.Web.Hostname)
	}
	if c.Store.Type != "postgres" {
		t.Errorf("Store.Type = %q", c.Store.Type)
	}
	if c.Store.DSN != "postgres://example/x" {
		t.Errorf("Store.DSN = %q (env not expanded?)", c.Store.DSN)
	}
}
