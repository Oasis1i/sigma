package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/tools"
)

func TestExploreReturnsSummaryAndStaysReadOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "explore", Arguments: `{"task":"what is in the workspace?"}`}}},
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write_file", Arguments: `{"path":"nope.txt","content":"x"}`}}},
		{Content: "note.txt contains hello. I could not write."},
		{Content: "the file note.txt says hello"},
	}}
	policy, err := approval.New("auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{Workspace: dir, Runner: &sandbox.Local{Dir: dir}}
	var ag *Agent
	env.Explore = func(ctx context.Context, task string) (string, error) {
		return ag.Explore(ctx, task)
	}
	ag = &Agent{
		Client:           script,
		Registry:         tools.NewRegistry(env),
		Policy:           policy,
		MaxSteps:         6,
		ExploreMaxSteps:  3,
		ExploreMaxTokens: 8000,
		Hooks:            &Hooks{},
	}
	var phases []string
	ag.Hooks.Subagent = func(ev SubagentEvent) { phases = append(phases, ev.Phase) }
	if err := ag.RunTurn(context.Background(), "look around"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("subagent wrote a file")
	}
	saw := false
	for _, batch := range script.Seen {
		for _, m := range batch {
			if m.Role == "tool" && strings.Contains(m.Content, "note.txt contains hello") {
				saw = true
			}
		}
	}
	if !saw {
		t.Fatal("parent did not receive the subagent summary")
	}
	if len(phases) == 0 || phases[0] != "start" || phases[len(phases)-1] != "end" {
		t.Fatalf("phases %v", phases)
	}
}

func TestExploreUsesSeparateClient(t *testing.T) {
	parent := &provider.Scripted{Responses: []provider.Response{{Content: "parent"}}}
	child := &provider.Scripted{Responses: []provider.Response{{Content: "from the child"}}}
	policy, err := approval.New("plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{Workspace: t.TempDir()}
	ag := &Agent{
		Client:           parent,
		ExploreClient:    child,
		Registry:         tools.NewRegistry(env),
		Policy:           policy,
		MaxSteps:         2,
		ExploreMaxSteps:  2,
		ExploreMaxTokens: 8000,
		Hooks:            &Hooks{},
	}
	summary, err := ag.Explore(context.Background(), "look")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "from the child") {
		t.Fatal(summary)
	}
	if len(parent.Seen) != 0 {
		t.Fatal("parent client was called")
	}
	if len(child.Seen) == 0 {
		t.Fatal("child client was not called")
	}
}

func TestExploreStopsAtTokenBudget(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "task", Arguments: `{"task":"find add"}`}}},
		{Content: "budget summary"},
		{Content: "parent done"},
	}}
	policy, err := approval.New("auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{Workspace: t.TempDir()}
	var ag *Agent
	env.Explore = func(ctx context.Context, task string) (string, error) {
		return ag.Explore(ctx, task)
	}
	ag = &Agent{
		Client:           script,
		Registry:         tools.NewRegistry(env),
		Policy:           policy,
		MaxSteps:         4,
		ExploreMaxSteps:  4,
		ExploreMaxTokens: 10,
	}
	if err := ag.RunTurn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(script.Seen) != 3 {
		t.Fatalf("calls %d, want parent + forced summary + parent", len(script.Seen))
	}
	if script.Seen[1][len(script.Seen[1])-1].Content != "Stop calling tools. Summarize what you found in one short paragraph." {
		t.Fatalf("summary prompt missing: %+v", script.Seen[1])
	}
}
