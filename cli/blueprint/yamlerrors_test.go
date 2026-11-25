package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadableYAMLErrors(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{
			"unknown field with a close match",
			"name: x\nagents:\n  - id: \"@a\"\n    promt: hi\n",
			`line 4: an agent has no field "promt" — did you mean "prompt"?`,
		},
		{
			"unknown field without a close match",
			"name: x\nagents:\n  - id: \"@a\"\n    colour: red\n",
			`line 4: an agent has no field "colour"`,
		},
		{
			"unknown top-level field",
			"name: x\nfurnitur: []\n",
			`line 2: the blueprint has no field "furnitur" — did you mean "furniture"?`,
		},
		{
			"text instead of a list",
			"name: x\nagents:\n  - id: \"@a\"\n    furniture: tasks\n",
			`line 4: expected a list, got the text "tasks"`,
		},
		{
			"text instead of a number, two errors",
			"name: x\nfloor_control:\n  max_agent_turns: lots\nagents:\n  - id: \"@a\"\n    enabled: maybe\n",
			"line 3: expected a number, got the text \"lots\"\nline 6: expected true or false, got the text \"maybe\"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "blueprint.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || err.Error() != c.want {
				t.Errorf("error = %v\nwant    %s", err, c.want)
			}
		})
	}
}
