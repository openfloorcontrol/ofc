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
