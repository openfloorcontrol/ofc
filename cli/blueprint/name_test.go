package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

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
