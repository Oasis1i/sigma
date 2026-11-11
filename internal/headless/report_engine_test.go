package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

func TestFakeProviderJSONReport(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "edit_file",
			Arguments: `{"path":"missing.go","old_string":"a","new_string":"b"}`,
		}}},
		{Content: "could not edit", Usage: provider.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
	}}
	eng := newTestEngine(t, "auto", script)
	eng.Submit(engine.Command{Kind: engine.CmdMessage, Text: "edit missing.go"})

	var report Report
	var kinds []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-eng.Events():
			report.Add(ev)
			kinds = append(kinds, string(ev.Kind))
			if ev.Kind == engine.KindDone {
				goto done
			}
		case <-deadline:
			t.Fatalf("timeout, kinds %s", strings.Join(kinds, ","))
		}
	}
done:
	if report.EditAttempts != 1 || report.EditFailures != 1 {
		t.Fatalf("edits %+v", report)
	}
	if report.Text != "could not edit" || report.PromptTokens != 5 || report.Steps < 1 {
		t.Fatalf("report %+v", report)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "tool_end") || !strings.Contains(joined, "done") {
		t.Fatalf("kinds %s", joined)
	}
}

func newTestEngine(t *testing.T, mode string, client provider.Client) *engine.Engine {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sessDir := filepath.Join(root, "sessions")
	policy, err := approval.New(mode, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := session.Create(sessDir, map[string]string{"model": "fake", "mode": mode})
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{
		Workspace:   ws,
		Runner:      &sandbox.Local{Dir: ws},
		BashTimeout: 5 * time.Second,
		Todos:       tools.NewTodos(),
	}
	ag := &agent.Agent{
		Client:           client,
		Policy:           policy,
		Session:          sess,
		Messages:         []provider.Message{{Role: "system", Content: "sys"}},
		MaxSteps:         8,
		Todos:            env.Todos,
		ExploreMaxSteps:  4,
		ExploreMaxTokens: 8000,
	}
	env.Explore = func(ctx context.Context, task string) (string, error) {
		return ag.Explore(ctx, task)
	}
	ag.Registry = tools.NewRegistry(env)
	if err := sess.AppendMessage(ag.Messages[0]); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(ag, engine.Options{
		SessionDir: sessDir,
		Model:      "fake",
		BaseURL:    "http://example.test/v1",
		Workspace:  ws,
	})
	t.Cleanup(eng.Close)
	return eng
}
