package furniture

import (
	"testing"
)

func TestWrapAsMCP(t *testing.T) {
	tb := NewTaskBoard()
	srv := WrapAsMCP(tb)
	if srv == nil {
		t.Fatal("WrapAsMCP returned nil")
	}
	// Basic smoke test — server was created with tools registered.
	// Full integration test via HTTP will be in the api package.
}
