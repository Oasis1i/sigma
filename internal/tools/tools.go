// Package tools is the six things the model is allowed to do.
//
// read_file, list_dir, and grep are read-only. edit_file, write_file, and bash
// can change the workspace. The registry does not check approval; the agent
// does that before Execute, so a tool stays easy to test on its own.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Oasis1i/sigma/internal/sandbox"
)

// Spec is the description sent to the model and the read-only bit used by
// the approval policy. Plan mode allows every tool with ReadOnly set.
type Spec struct {
	Name        string
	Description string
	Parameters  map[string]any
	ReadOnly    bool
}

// Tool is one callable function.
type Tool interface {
	Spec() Spec
	Execute(ctx context.Context, env *Env, args json.RawMessage) (string, error)
}

// Env is the workspace a tool runs against. Runner is only required by bash.
type Env struct {
	Workspace   string
	Runner      sandbox.Runner
	BashTimeout time.Duration
}

// Registry holds the tools in the order they are shown to the model.
type Registry struct {
	env    *Env
	list   []Tool
	byName map[string]Tool
}

// NewRegistry registers the built-in tools. env.Workspace must be absolute.
func NewRegistry(env *Env) *Registry {
	r := &Registry{
		env:    env,
		byName: map[string]Tool{},
	}
	for _, t := range []Tool{
		&readFile{},
		&listDir{},
		&grepTool{},
		&editFile{},
		&writeFile{},
		&bashTool{},
	} {
		r.list = append(r.list, t)
		r.byName[t.Spec().Name] = t
	}
	return r
}

// Specs returns every tool schema.
func (r *Registry) Specs() []Spec {
	out := make([]Spec, 0, len(r.list))
	for _, t := range r.list {
		out = append(out, t.Spec())
	}
	return out
}

// Get returns the spec for a tool name.
func (r *Registry) Get(name string) (Spec, bool) {
	t, ok := r.byName[name]
	if !ok {
		return Spec{}, false
	}
	return t.Spec(), true
}

// Execute runs a tool. Unknown tools and bad JSON are errors the agent
// turns into a tool result so the model can recover.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return t.Execute(ctx, r.env, args)
}
