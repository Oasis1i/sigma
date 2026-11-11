package engine

import (
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/provider"
)

func TestPlanApprovalThenExecute(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "t1", Name: "todo_write",
			Arguments: `{"todos":[{"id":"1","content":"fix add","status":"pending"}]}`,
		}}},
		{Content: "plan ready"},
		{Content: "executed the plan"},
	}}
	eng, _ := newEngine(t, "auto", script)
	if _, err := eng.ApplySlash("/mode plan"); err != nil {
		t.Fatal(err)
	}
	eng.Submit(Command{Kind: CmdMessage, Text: "plan the fix"})
	var sawTodo, sawPlan bool
	evs := collect(t, eng, func(ev Event) {
		if ev.Kind == KindTodo && len(ev.Todos) == 1 && ev.Todos[0].Content == "fix add" {
			sawTodo = true
		}
		if ev.Kind == KindApproval && ev.Tool == "plan" {
			sawPlan = true
			if !strings.Contains(ev.Args, "fix add") {
				t.Errorf("plan args %q", ev.Args)
			}
			eng.Submit(Command{Kind: CmdApproval, ApprovalID: ev.ApprovalID, Allow: true})
		}
	})
	if !sawTodo || !sawPlan {
		t.Fatalf("todo %v plan %v kinds %s", sawTodo, sawPlan, joinKinds(evs))
	}
	if eng.Mode() != "auto" {
		t.Fatalf("execution mode %s, want the mode from before plan", eng.Mode())
	}
	joined := ""
	for _, batch := range script.Seen {
		for _, m := range batch {
			joined += m.Content
		}
	}
	if !strings.Contains(joined, "approved the plan") {
		t.Fatal("execution turn did not see the approval")
	}
}

func TestPlanRejectDoesNotExecute(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "t1", Name: "todo_write",
			Arguments: `{"todos":[{"content":"do the thing"}]}`,
		}}},
		{Content: "ready"},
		{Content: "should not run"},
	}}
	eng, _ := newEngine(t, "plan", script)
	eng.Submit(Command{Kind: CmdMessage, Text: "make a plan"})
	evs := collect(t, eng, func(ev Event) {
		if ev.Kind == KindApproval && ev.Tool == "plan" {
			eng.Submit(Command{Kind: CmdApproval, ApprovalID: ev.ApprovalID, Allow: false})
		}
	})
	if eng.Mode() != "plan" {
		t.Fatalf("mode %s", eng.Mode())
	}
	if len(script.Seen) != 2 {
		t.Fatalf("calls %d, rejection should not start execution", len(script.Seen))
	}
	if !strings.Contains(joinKinds(evs), "notice") {
		t.Fatalf("kinds %s", joinKinds(evs))
	}
}
