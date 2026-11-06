package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func agentIDs(bp *Blueprint) []string {
	var ids []string
	for _, a := range bp.Agents {
		ids = append(ids, a.ID)
	}
	return ids
}

// A disabled entry is dropped before anything looks at it: its unset
// variables, missing furniture and missing ref file don't fail the load.
func TestDisabledEntriesAreDropped(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "off.agent.yaml"), []byte("id: \"@off-in-file\"\nenabled: false\n"), 0o644)
	path := filepath.Join(dir, "blueprint.yaml")
	os.WriteFile(path, []byte(`name: t
furniture:
  - name: tasks
    type: taskboard
  - name: kb
    type: mcp
    url: ${OFC_TEST_SURELY_UNSET}
    enabled: false
agents:
  - id: "@on"
    furniture: [tasks]
  - id: "@off"
    enabled: false
    prompt: ${OFC_TEST_SURELY_UNSET}
    furniture: [kb]
  - ref: ./missing.agent.yaml
    enabled: false
  - ref: ./off.agent.yaml
  - id: "@explicit"
    enabled: true
`), 0o644)

	bp, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := agentIDs(bp); len(got) != 2 || got[0] != "@on" || got[1] != "@explicit" {
		t.Errorf("agents = %v, want [@on @explicit]", got)
	}
	if len(bp.Furniture) != 1 || bp.Furniture[0].Name != "tasks" {
		t.Errorf("furniture = %+v, want only tasks", bp.Furniture)
	}
}

// An enabled agent that lists disabled furniture fails, as for any
// furniture that is not on the floor.
func TestAgentListingDisabledFurnitureFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blueprint.yaml")
	os.WriteFile(path, []byte(`name: t
furniture:
  - name: tasks
    type: taskboard
    enabled: false
agents:
  - id: "@a"
    furniture: [tasks]
`), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("want an error for furniture that is disabled")
	}
}
