package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPromptTemplate_NoMarker(t *testing.T) {
	// No <% marker → returns unchanged, no template parsing
	in := "Hello {{ this }} should pass through unchanged"
	out, err := expandPromptTemplate(in, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != in {
		t.Errorf("expected unchanged, got: %q", out)
	}
}

func TestExpandPromptTemplate_Readfile(t *testing.T) {
	dir := t.TempDir()
	contentPath := filepath.Join(dir, "catalog.md")
	if err := os.WriteFile(contentPath, []byte("- Product A\n- Product B\n"), 0644); err != nil {
		t.Fatal(err)
	}

	prompt := "Available:\n<% readfile \"catalog.md\" %>"
	out, err := expandPromptTemplate(prompt, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "- Product A") {
		t.Errorf("expected catalog content in output, got: %q", out)
	}
}

func TestExpandPromptTemplate_ReadfileMissing(t *testing.T) {
	prompt := `<% readfile "nonexistent.md" %>`
	_, err := expandPromptTemplate(prompt, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestExpandPromptTemplate_Env(t *testing.T) {
	t.Setenv("OFC_TEST_VAR", "hello")
	prompt := `<% env "OFC_TEST_VAR" %>`
	out, err := expandPromptTemplate(prompt, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("expected 'hello', got: %q", out)
	}
}

func TestExpandPromptTemplate_ParseError(t *testing.T) {
	// Unclosed action
	prompt := `<% readfile "x" `
	_, err := expandPromptTemplate(prompt, "")
	if err == nil {
		t.Fatal("expected parse error, got nil")
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

func TestLoad_PromptTemplate(t *testing.T) {
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog.md")
	if err := os.WriteFile(catalog, []byte("CATALOG-CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	bpYAML := `name: test
agents:
  - id: "@a"
    prompt: |
      You are an agent.
      <% readfile "catalog.md" %>
`
	bpPath := filepath.Join(dir, "blueprint.yaml")
	if err := os.WriteFile(bpPath, []byte(bpYAML), 0644); err != nil {
		t.Fatal(err)
	}

	bp, err := Load(bpPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(bp.Agents[0].Prompt, "CATALOG-CONTENT") {
		t.Errorf("expected catalog content in prompt, got: %q", bp.Agents[0].Prompt)
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
