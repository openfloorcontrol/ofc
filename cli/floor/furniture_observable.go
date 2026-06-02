package floor

import (
	"fmt"

	"github.com/openfloorcontrol/ofc/furniture"
)

// observableFurniture wraps a Furniture and emits FurnitureUpdated events
// on mutating Call()s. This covers all paths: LLM agents, ACP/MCP, and web UI.
type observableFurniture struct {
	inner furniture.Furniture
	chat  *Room
}

// readOnlyTools are tool names that don't mutate state (no need to notify).
// Covers both built-in (taskboard) and common external (filesystem MCP) tools.
//
// TODO: this is an allowlist that has to grow as new MCP servers ship with
// new read-only tools. Eventually furniture types should declare which
// tools are read-only themselves, rather than us guessing by name.
var readOnlyTools = map[string]bool{
	"list_tasks":                true,
	"get_task":                  true,
	"list_directory":            true,
	"list_directory_with_sizes": true,
	"directory_tree":            true,
	"read_file":                 true,
	"read_text_file":            true,
	"read_media_file":           true,
	"read_multiple_files":       true,
	"search_files":              true,
	"get_file_info":             true,
	"list_allowed_directories":  true,
}

func (o *observableFurniture) Name() string           { return o.inner.Name() }
func (o *observableFurniture) Tools() []furniture.Tool { return o.inner.Tools() }

// ReadFileRaw forwards to the inner furniture if it implements FileReader.
func (o *observableFurniture) ReadFileRaw(path string) ([]byte, string, error) {
	if fr, ok := o.inner.(furniture.FileReader); ok {
		return fr.ReadFileRaw(path)
	}
	return nil, "", fmt.Errorf("furniture %q does not support file reading", o.inner.Name())
}

func (o *observableFurniture) Call(toolName string, args map[string]interface{}) (interface{}, error) {
	result, err := o.inner.Call(toolName, args)
	if err == nil && !readOnlyTools[toolName] {
		o.chat.PostStream(FurnitureUpdated{Name: o.inner.Name()})
	}
	return result, err
}
