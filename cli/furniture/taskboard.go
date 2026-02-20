package furniture

import (
	"fmt"
	"sync"
)

// Task represents a single item on the task board.
type Task struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Assignee    string `json:"assignee,omitempty"`
}

// TaskBoard is a shared task board that agents can read and write.
type TaskBoard struct {
	mu     sync.RWMutex
	tasks  []Task
	nextID int
}

// NewTaskBoard creates an empty task board.
func NewTaskBoard() *TaskBoard {
	return &TaskBoard{nextID: 1}
}

func (tb *TaskBoard) Name() string { return "tasks" }

func (tb *TaskBoard) Tools() []Tool {
	return []Tool{
		{
			Name:        "list_tasks",
			Description: "List all tasks on the board, optionally filtered by status.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status (todo, in_progress, done). Omit for all tasks.",
					},
				},
			},
		},
		{
			Name:        "add_task",
			Description: "Add a new task to the board.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Task title",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Task description (optional)",
					},
				},
				"required": []string{"title"},
			},
		},
		{
			Name:        "update_task",
			Description: "Update an existing task's status, assignee, or other fields.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "Task ID to update",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "New status (todo, in_progress, done)",
					},
					"assignee": map[string]interface{}{
						"type":        "string",
						"description": "Assign to an agent (e.g. @coder)",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "New title",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "New description",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "get_task",
			Description: "Get details of a specific task by ID.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "Task ID",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (tb *TaskBoard) Call(toolName string, args map[string]interface{}) (interface{}, error) {
	switch toolName {
	case "list_tasks":
		return tb.listTasks(args)
	case "add_task":
		return tb.addTask(args)
	case "update_task":
		return tb.updateTask(args)
	case "get_task":
		return tb.getTask(args)
	default:
		return nil, &ErrUnknownTool{Furniture: tb.Name(), Tool: toolName}
	}
}

func (tb *TaskBoard) listTasks(args map[string]interface{}) (interface{}, error) {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	statusFilter, _ := args["status"].(string)

	var result []Task
	for _, t := range tb.tasks {
		if statusFilter == "" || t.Status == statusFilter {
			result = append(result, t)
		}
	}
	if result == nil {
		result = []Task{}
	}
	return map[string]interface{}{
		"tasks": result,
		"count": len(result),
	}, nil
}

func (tb *TaskBoard) addTask(args map[string]interface{}) (interface{}, error) {
	title, _ := args["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	desc, _ := args["description"].(string)

	tb.mu.Lock()
	defer tb.mu.Unlock()

	task := Task{
		ID:          tb.nextID,
		Title:       title,
		Description: desc,
		Status:      "todo",
	}
	tb.nextID++
	tb.tasks = append(tb.tasks, task)

	return task, nil
}

func (tb *TaskBoard) updateTask(args map[string]interface{}) (interface{}, error) {
	id, err := intArg(args, "id")
	if err != nil {
		return nil, err
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	for i := range tb.tasks {
		if tb.tasks[i].ID == id {
			if s, ok := args["status"].(string); ok {
				tb.tasks[i].Status = s
			}
			if s, ok := args["assignee"].(string); ok {
				tb.tasks[i].Assignee = s
			}
			if s, ok := args["title"].(string); ok {
				tb.tasks[i].Title = s
			}
			if s, ok := args["description"].(string); ok {
				tb.tasks[i].Description = s
			}
			return tb.tasks[i], nil
		}
	}

	return nil, fmt.Errorf("task %d not found", id)
}

func (tb *TaskBoard) getTask(args map[string]interface{}) (interface{}, error) {
	id, err := intArg(args, "id")
	if err != nil {
		return nil, err
	}

	tb.mu.RLock()
	defer tb.mu.RUnlock()

	for _, t := range tb.tasks {
		if t.ID == id {
			return t, nil
		}
	}

	return nil, fmt.Errorf("task %d not found", id)
}

// intArg extracts an integer argument, handling JSON number types.
func intArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be an integer, got %T", key, v)
	}
}
