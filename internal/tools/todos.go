package tools

import "sync"

// Todo is one item in the plan. Status is pending, in_progress, or completed.
type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Todos is the in-memory plan. It is not the workspace; plan mode may update
// it, and execution mode marks items done. OnChange runs after each replace
// so the engine can emit a todo event.
type Todos struct {
	mu       sync.Mutex
	items    []Todo
	onChange func([]Todo)
}

// NewTodos returns an empty list.
func NewTodos() *Todos { return &Todos{} }

// SetOnChange registers the listener. It may be nil.
func (t *Todos) SetOnChange(fn func([]Todo)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.onChange = fn
	t.mu.Unlock()
}

// Snapshot returns a copy of the current list.
func (t *Todos) Snapshot() []Todo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Todo(nil), t.items...)
}

// Replace stores a new list and notifies the listener with a copy.
func (t *Todos) Replace(items []Todo) []Todo {
	cp := append([]Todo(nil), items...)
	t.mu.Lock()
	t.items = append([]Todo(nil), cp...)
	fn := t.onChange
	t.mu.Unlock()
	if fn != nil {
		fn(append([]Todo(nil), cp...))
	}
	return cp
}
