package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/session"
)

func TestSplitHistoryKeepsToolPair(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "doing", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "bash", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
		{Role: "user", Content: "new"},
	}
	// keep=2 would start the tail on the tool result. The cut moves left to
	// include the assistant message that requested it.
	_, head, tail := splitHistory(msgs, 2)
	if len(head) != 1 || head[0].Content != "old" {
		t.Fatalf("head %+v", head)
	}
	if len(tail) != 3 || tail[0].Role != "assistant" || tail[1].Role != "tool" {
		t.Fatalf("tail %+v", tail)
	}
}

func TestCompactRewritesHistoryAndLog(t *testing.T) {
	long := strings.Repeat("alpha bravo charlie ", 40)
	script := &provider.Scripted{Responses: []provider.Response{
		{Content: "SUMMARY-TOKEN the user was editing files"},
		{Content: "done"},
	}}
	ag, _ := newTestAgent(t, "auto", nil, script)
	ag.CompactThreshold = 40
	ag.Messages = []provider.Message{{Role: "system", Content: "sys"}}
	if err := ag.Session.AppendMessage(ag.Messages[0]); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		m := provider.Message{Role: "user", Content: long}
		ag.Messages = append(ag.Messages, m)
		if err := ag.Session.AppendMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if EstimateTokens(ag.Messages) <= ag.CompactThreshold {
		t.Fatalf("fixture is not over the threshold: %d", EstimateTokens(ag.Messages))
	}
	if err := ag.RunTurn(context.Background(), "continue please"); err != nil {
		t.Fatal(err)
	}
	var sawSummary, sawTail bool
	for _, m := range ag.Messages {
		if strings.Contains(m.Content, "SUMMARY-TOKEN") {
			sawSummary = true
		}
		if m.Content == "continue please" {
			sawTail = true
		}
	}
	if !sawSummary || !sawTail {
		t.Fatalf("messages missing summary or the new request: %+v", ag.Messages)
	}
	if len(ag.Messages) >= 12 {
		t.Fatalf("history was not shortened: %d", len(ag.Messages))
	}
	loaded, err := ag.Session.Messages()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(ag.Messages) {
		t.Fatalf("log replay %d in-memory %d", len(loaded), len(ag.Messages))
	}
	for i := range loaded {
		if loaded[i].Role != ag.Messages[i].Role || loaded[i].Content != ag.Messages[i].Content {
			t.Fatalf("replay %d log %+v mem %+v", i, loaded[i], ag.Messages[i])
		}
	}
	events, err := ag.Session.Events()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == session.TypeCompaction && e.Compaction != nil && e.Compaction.Dropped > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("session log has no compaction event")
	}
	// The summary call is not the user-visible reply.
	if script.Responses[1].Content != "done" {
		t.Fatal("fixture")
	}
	last := ag.Messages[len(ag.Messages)-1]
	if last.Role != "assistant" || last.Content != "done" {
		t.Fatalf("final reply %+v", last)
	}
}
