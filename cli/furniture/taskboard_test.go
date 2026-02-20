package furniture

import (
	"testing"
)

func TestTaskBoardCRUD(t *testing.T) {
	tb := NewTaskBoard()

	// Add tasks
	result, err := tb.Call("add_task", map[string]interface{}{
		"title":       "Design API",
		"description": "Design the REST API endpoints",
	})
	if err != nil {
		t.Fatalf("add_task: %v", err)
	}
	task := result.(Task)
	if task.ID != 1 || task.Title != "Design API" || task.Status != "todo" {
		t.Fatalf("unexpected task: %+v", task)
	}

	_, err = tb.Call("add_task", map[string]interface{}{
		"title": "Write tests",
	})
	if err != nil {
		t.Fatalf("add_task: %v", err)
	}

	// List all
	result, err = tb.Call("list_tasks", map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}
	listing := result.(map[string]interface{})
	if listing["count"] != 2 {
		t.Fatalf("expected 2 tasks, got %v", listing["count"])
	}

	// Update
	result, err = tb.Call("update_task", map[string]interface{}{
		"id":       float64(1), // JSON numbers are float64
		"status":   "in_progress",
		"assignee": "@coder",
	})
	if err != nil {
		t.Fatalf("update_task: %v", err)
	}
	updated := result.(Task)
	if updated.Status != "in_progress" || updated.Assignee != "@coder" {
		t.Fatalf("unexpected updated task: %+v", updated)
	}

	// Get
	result, err = tb.Call("get_task", map[string]interface{}{"id": float64(1)})
	if err != nil {
		t.Fatalf("get_task: %v", err)
	}
	got := result.(Task)
	if got.Assignee != "@coder" {
		t.Fatalf("expected @coder assignee, got %s", got.Assignee)
	}

	// List with filter
	result, err = tb.Call("list_tasks", map[string]interface{}{"status": "todo"})
	if err != nil {
		t.Fatalf("list_tasks filtered: %v", err)
	}
	filtered := result.(map[string]interface{})
	if filtered["count"] != 1 {
		t.Fatalf("expected 1 todo task, got %v", filtered["count"])
	}

	// Unknown tool
	_, err = tb.Call("delete_task", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
