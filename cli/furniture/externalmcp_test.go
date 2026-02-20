package furniture

import (
	"context"
	"os/exec"
	"testing"
)

func TestExternalMCP_Everything(t *testing.T) {
	// Skip if npx is not available
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not found, skipping external MCP test")
	}

	ctx := context.Background()
	ext, err := NewExternalMCP(ctx, "everything", "npx", []string{"-y", "@modelcontextprotocol/server-everything"})
	if err != nil {
		t.Fatalf("NewExternalMCP: %v", err)
	}
	defer ext.Close()

	// Should have discovered tools
	tools := ext.Tools()
	if len(tools) == 0 {
		t.Fatal("expected tools from everything server, got none")
	}
	t.Logf("discovered %d tools:", len(tools))
	for _, tool := range tools {
		t.Logf("  - %s: %s", tool.Name, tool.Description)
	}

	// Call the echo tool
	result, err := ext.Call("echo", map[string]interface{}{
		"message": "hello from ofc",
	})
	if err != nil {
		t.Fatalf("Call echo: %v", err)
	}
	t.Logf("echo result: %v", result)

	if str, ok := result.(string); ok {
		if str == "" {
			t.Error("expected non-empty echo result")
		}
	}
}
