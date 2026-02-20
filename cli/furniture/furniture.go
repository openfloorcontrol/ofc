// Package furniture defines the interface for shared interactive objects on the floor.
package furniture

import "fmt"

// Tool describes a single capability offered by a piece of furniture.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

// Furniture is the interface for all furniture implementations.
// Furniture are shared objects on the floor (task boards, file cabinets, etc.)
// that agents can interact with via named tool calls.
type Furniture interface {
	// Name returns the furniture identifier (e.g. "tasks").
	Name() string

	// Tools returns the list of tools this furniture provides.
	Tools() []Tool

	// Call invokes a tool by name with JSON-compatible arguments.
	// Returns a JSON-serializable result.
	Call(toolName string, args map[string]interface{}) (interface{}, error)
}

// ErrUnknownTool is returned when a tool name is not recognized.
type ErrUnknownTool struct {
	Furniture string
	Tool      string
}

func (e *ErrUnknownTool) Error() string {
	return fmt.Sprintf("furniture %q has no tool %q", e.Furniture, e.Tool)
}
