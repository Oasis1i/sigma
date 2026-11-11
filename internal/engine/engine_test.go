package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

func newEngine(t *testing.T, mode string, client provider.Client) (*Engine, string) {
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
	eng := New(ag, Options{
		SessionDir: sessDir,
		Model:      "fake",
		BaseURL:    "http://example.test/v1",
		Workspace:  ws,
	})
	t.Cleanup(eng.Close)
	return eng, ws
}

func collect(t *testing.T, eng *Engine, on func(Event)) []Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var out []Event
	for {
		select {
		case ev := <-eng.Events():
			out = append(out, ev)
			if on != nil {
				on(ev)
			}
			if ev.Kind == KindDone {
				return out
			}
		case <-deadline:
			t.Fatalf("timeout waiting for done, events: %s", joinKinds(out))
		}
	}
}

func joinKinds(evs []Event) string {
	parts := make([]string, len(evs))
	for i, ev := range evs {
		parts[i] = string(ev.Kind)
	}
	return strings.Join(parts, ",")
}

func TestTextAndUsageStream(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{{
		Content: "hello",
		Usage:   provider.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}}}
	eng, _ := newEngine(t, "auto", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "say hi"})
	evs := collect(t, eng, nil)
	if got := joinKinds(evs); got != "step,text_delta,usage,done" {
		t.Fatalf("kinds: %s", got)
	}
	if evs[1].Text != "hello" {
		t.Fatalf("delta %q", evs[1].Text)
	}
	if evs[2].TotalTokens != 5 || evs[2].PromptTokens != 3 {
		t.Fatalf("usage %+v", evs[2])
	}
	p, c := eng.Usage()
	if p != 3 || c != 2 {
		t.Fatalf("totals %d %d", p, c)
	}
	if evs[len(evs)-1].Canceled {
		t.Fatal("done was canceled")
	}
}

func TestToolStartAndFinish(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"hello.txt","content":"hi\n"}`,
		}}},
		{Content: "created hello.txt"},
	}}
	eng, ws := newEngine(t, "auto", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "write hello"})
	evs := collect(t, eng, nil)
	if got := joinKinds(evs); got != "step,tool_start,tool_end,step,text_delta,done" {
		t.Fatalf("kinds: %s", got)
	}
	start, end := evs[1], evs[2]
	if start.Tool != "write_file" || start.CallID != "c1" {
		t.Fatalf("start %+v", start)
	}
	if !strings.Contains(start.Args, "hello.txt") || start.Preview != "hello.txt" {
		t.Fatalf("args %s preview %s", start.Args, start.Preview)
	}
	if end.Failed || !strings.Contains(end.Output, "wrote") {
		t.Fatalf("end %+v", end)
	}
	body, err := os.ReadFile(filepath.Join(ws, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi\n" {
		t.Fatalf("file %q", body)
	}
}

func TestEditDiffOnToolEvent(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "edit_file",
			Arguments: `{"path":"a.go","old_string":"return a - b","new_string":"return a + b"}`,
		}}},
		{Content: "could not edit"},
	}}
	eng, _ := newEngine(t, "auto", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "fix add"})
	var diff string
	evs := collect(t, eng, func(ev Event) {
		if ev.Kind == KindToolStart {
			diff = ev.Diff
		}
	})
	if !strings.Contains(diff, "-return a - b") || !strings.Contains(diff, "+return a + b") {
		t.Fatalf("diff:\n%s", diff)
	}
	// The file does not exist, so the edit fails, but the diff was still shown.
	failed := false
	for _, ev := range evs {
		if ev.Kind == KindToolEnd && ev.Failed && strings.Contains(ev.Output, "does not exist") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("events: %s", joinKinds(evs))
	}
}

func TestApprovalAllowAndDeny(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"nope.txt","content":"x"}`,
		}}},
		{Content: "denied, stopping"},
	}}
	eng, ws := newEngine(t, "ask", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "write"})
	sawApproval := false
	evs := collect(t, eng, func(ev Event) {
		if ev.Kind != KindApproval {
			return
		}
		sawApproval = true
		if _, err := os.Stat(filepath.Join(ws, "nope.txt")); !os.IsNotExist(err) {
			t.Errorf("file existed before approval: %v", err)
		}
		eng.Submit(Command{Kind: CmdApproval, ApprovalID: ev.ApprovalID, Allow: false})
	})
	if !sawApproval {
		t.Fatal("no approval event")
	}
	if _, err := os.Stat(filepath.Join(ws, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write still created a file")
	}
	denied := false
	for _, ev := range evs {
		if ev.Kind == KindToolEnd && ev.Failed && strings.Contains(ev.Output, "denied") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("events: %+v", evs)
	}

	script = &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"yes.txt","content":"ok"}`,
		}}},
		{Content: "wrote it"},
	}}
	eng, ws = newEngine(t, "ask", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "write"})
	order := collect(t, eng, func(ev Event) {
		if ev.Kind == KindApproval {
			eng.Submit(Command{Kind: CmdApproval, ApprovalID: ev.ApprovalID, Allow: true})
		}
	})
	if got := joinKinds(order); !strings.Contains(got, "tool_start,approval_request,tool_end") {
		t.Fatalf("order: %s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "yes.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyDoesNotAsk(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "list_dir", Arguments: `{"path":"."}`,
		}}},
		{Content: "listed"},
	}}
	eng, _ := newEngine(t, "ask", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "list"})
	evs := collect(t, eng, func(ev Event) {
		if ev.Kind == KindApproval {
			t.Errorf("read-only tool asked for approval: %+v", ev)
		}
	})
	if strings.Contains(joinKinds(evs), "approval") {
		t.Fatalf("kinds: %s", joinKinds(evs))
	}
}

func TestCancel(t *testing.T) {
	started := make(chan struct{})
	client := gateClient{started: started}
	eng, _ := newEngine(t, "auto", client)
	eng.Submit(Command{Kind: CmdMessage, Text: "hang"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not reach the provider")
	}
	eng.Cancel()
	evs := collect(t, eng, nil)
	last := evs[len(evs)-1]
	if last.Kind != KindDone || !last.Canceled {
		t.Fatalf("last %+v kinds %s", last, joinKinds(evs))
	}
	for _, ev := range evs {
		if ev.Kind == KindError {
			t.Fatalf("cancel surfaced as an error: %+v", ev)
		}
	}
}

func TestProviderError(t *testing.T) {
	eng, _ := newEngine(t, "auto", errClient{})
	eng.Submit(Command{Kind: CmdMessage, Text: "hi"})
	evs := collect(t, eng, nil)
	if got := joinKinds(evs); got != "step,error,done" {
		t.Fatalf("kinds: %s", got)
	}
	if evs[1].Text != "boom" || evs[2].Canceled {
		t.Fatalf("%+v %+v", evs[1], evs[2])
	}
}

func TestSetModeUpdatesSystemPrompt(t *testing.T) {
	eng, _ := newEngine(t, "ask", &provider.Scripted{})
	eng.ag.Messages = []provider.Message{{Role: "system", Content: "You are Sigma\nApproval mode: ask\n"}}
	if err := eng.ag.Session.AppendMessage(eng.ag.Messages[0]); err != nil {
		t.Fatal(err)
	}
	before := len(eng.Messages())
	effect, err := eng.ApplySlash("/mode auto")
	if err != nil {
		t.Fatal(err)
	}
	if effect.Mode != "auto" || eng.Mode() != "auto" {
		t.Fatalf("mode %s %+v", eng.Mode(), effect)
	}
	msgs := eng.Messages()
	if len(msgs) != before {
		t.Fatalf("mode change added a turn: before %d after %d %+v", before, len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.Role == "user" {
			t.Fatalf("user message in transcript: %+v", m)
		}
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "Approval mode: auto") {
		t.Fatalf("system prompt: %s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Approval mode: ask") {
		t.Fatalf("old mode still in the system prompt: %s", msgs[0].Content)
	}
	loaded, err := eng.ag.Session.Messages()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || !strings.Contains(loaded[0].Content, "Approval mode: auto") {
		t.Fatalf("resume transcript: %+v", loaded)
	}
}

func TestSlashModeClearResume(t *testing.T) {
	eng, _ := newEngine(t, "ask", &provider.Scripted{})
	first := eng.SessionID()
	effect, err := eng.ApplySlash("/mode auto")
	if err != nil {
		t.Fatal(err)
	}
	if effect.Mode != "auto" || eng.Mode() != "auto" {
		t.Fatalf("mode %s effect %+v", eng.Mode(), effect)
	}
	if _, err := eng.ApplySlash("/mode sideways"); err == nil {
		t.Fatal("expected bad mode")
	}
	if _, err := eng.ApplySlash("/nope"); err == nil {
		t.Fatal("expected unknown command")
	}
	effect, err = eng.ApplySlash("/clear")
	if err != nil {
		t.Fatal(err)
	}
	if !effect.Rebuild || eng.SessionID() == first || eng.SessionID() == "" {
		t.Fatalf("clear %+v id %s", effect, eng.SessionID())
	}
	msgs := eng.Messages()
	if len(msgs) != 1 || msgs[0].Role != "system" {
		t.Fatalf("cleared messages: %+v", msgs)
	}
	listEffect, err := eng.ApplySlash("/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listEffect.Notice, first) || !strings.Contains(listEffect.Notice, eng.SessionID()) {
		t.Fatalf("sessions:\n%s", listEffect.Notice)
	}
	effect, err = eng.ApplySlash("/resume " + first)
	if err != nil {
		t.Fatal(err)
	}
	if !effect.Rebuild || eng.SessionID() != first {
		t.Fatalf("resume %+v id %s want %s", effect, eng.SessionID(), first)
	}
	quit, err := eng.ApplySlash("/quit")
	if err != nil || !quit.Quit {
		t.Fatalf("quit %+v %v", quit, err)
	}
}

type gateClient struct {
	started chan struct{}
}

func (g gateClient) Complete(ctx context.Context, _ []provider.Message, _ []provider.ToolSpec, _ func(string)) (provider.Response, error) {
	select {
	case <-g.started:
	default:
		close(g.started)
	}
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

type errClient struct{}

func (errClient) Complete(context.Context, []provider.Message, []provider.ToolSpec, func(string)) (provider.Response, error) {
	return provider.Response{}, errString("boom")
}

type errString string

func (e errString) Error() string { return string(e) }
