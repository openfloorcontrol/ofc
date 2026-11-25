package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session recorded before meta.floor belongs to the floor named like
// its blueprint once the store is opened; the migration runs only once.
func TestJSONLMigrateMetaFloor(t *testing.T) {
	dir := t.TempDir()
	old := `{"kind":"meta","session_id":"s1","meta":{"blueprint_name":"shack","cwd":"/x"}}` + "\n"
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	// A session without meta stays as it is.
	if err := os.WriteFile(filepath.Join(dir, "s2.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewJSONL(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.GetMeta("s1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Floor != "shack" || meta.BlueprintName != "shack" || meta.CWD != "/x" {
		t.Errorf("meta = %+v, want floor and blueprint shack, cwd /x", meta)
	}
	s.Close()

	version, err := os.ReadFile(filepath.Join(dir, schemaVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != "1" {
		t.Errorf("schema_version = %q, want 1", version)
	}

	// Reopening applies nothing again: the file keeps its two lines.
	s, err = NewJSONL(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 2 {
		t.Errorf("s1.jsonl has %d lines, want 2:\n%s", n, data)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "s2.jsonl")); len(data) != 0 {
		t.Errorf("s2.jsonl = %q, want empty", data)
	}
}

func TestJSONLRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, schemaVersionFile), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJSONL(dir); err == nil {
		t.Fatal("NewJSONL opened a directory with a newer schema version")
	}
}
