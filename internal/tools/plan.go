package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type todoWrite struct{}

func (todoWrite) Spec() Spec {
	return Spec{
		Name: "todo_write",
		// ReadOnly is set so plan mode, which refuses workspace edits, still
		// allows the plan itself. The list lives in memory, not in a file.
		Description: "Replace the plan todo list. Each item has an id, content, and status " +
			"(pending, in_progress, or completed). In plan mode this is how you publish the plan. " +
			"During execution, update statuses as you finish items. This does not edit workspace files.",
		Parameters: objectSchema([]string{"todos"}, map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      strProp("Stable id, such as 1 or add-fix"),
						"content": strProp("What to do, in one sentence"),
						"status":  strProp("pending, in_progress, or completed"),
					},
					"required": []string{"content"},
				},
			},
		}),
		ReadOnly: true,
	}
}

func (todoWrite) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if env.Todos == nil {
		return "", fmt.Errorf("todo list is not available")
	}
	var args struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if len(args.Todos) == 0 {
		return "", fmt.Errorf("todos must contain at least one item")
	}
	if len(args.Todos) > 20 {
		return "", fmt.Errorf("todos is limited to 20 items")
	}
	items := make([]Todo, 0, len(args.Todos))
	for i, item := range args.Todos {
		item.Content = strings.TrimSpace(item.Content)
		if item.Content == "" {
			return "", fmt.Errorf("todo %d has empty content", i+1)
		}
		item.Status = normalizeStatus(item.Status)
		if item.ID == "" {
			item.ID = fmt.Sprintf("%d", i+1)
		}
		items = append(items, item)
	}
	env.Todos.Replace(items)
	return fmt.Sprintf("updated %d todos", len(items)), nil
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in_progress", "in progress", "doing":
		return "in_progress"
	case "completed", "done":
		return "completed"
	default:
		return "pending"
	}
}

// ExploreFunc runs a read-only subagent. The parent agent installs it.
// A nil func means the tool is registered but this process did not wire it.
type ExploreFunc func(ctx context.Context, task string) (string, error)

type exploreTool struct {
	name string
}

func (t exploreTool) Spec() Spec {
	return Spec{
		Name: t.name,
		Description: "Spawn a read-only subagent (the task/explore tool) with its own context. " +
			"It can read_file, list_dir, and grep, and it returns a short summary. " +
			"It cannot edit files or run bash. Use it for a focused question so the parent transcript stays small.",
		Parameters: objectSchema([]string{"task"}, map[string]any{
			"task": strProp("What the subagent should find out. One concrete question."),
		}),
		ReadOnly: true,
	}
}

func (t exploreTool) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if env.Explore == nil {
		return "", fmt.Errorf("explore is not available")
	}
	var args struct {
		Task   string `json:"task"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	task := strings.TrimSpace(args.Task)
	if task == "" {
		task = strings.TrimSpace(args.Prompt)
	}
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	summary, err := env.Explore(ctx, task)
	if err != nil && strings.TrimSpace(summary) == "" {
		return "", err
	}
	if strings.TrimSpace(summary) == "" {
		return "", fmt.Errorf("subagent returned an empty summary")
	}
	return summary, nil
}
