package headless

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/engine"
)

func TestReportCountsEditsAndTokens(t *testing.T) {
	var r Report
	events := []engine.Event{
		{Kind: engine.KindStep, Step: 1, MaxSteps: 8},
		{Kind: engine.KindToolEnd, Tool: "edit_file", Failed: true, Output: "Error: matched 0"},
		{Kind: engine.KindUsage, PromptTokens: 10, CompletionTokens: 4},
		{Kind: engine.KindStep, Step: 2, MaxSteps: 8},
		{Kind: engine.KindToolEnd, Tool: "write_file", Output: "wrote a.go"},
		{Kind: engine.KindTextDelta, Text: "fixed"},
		{Kind: engine.KindDone},
	}
	for _, ev := range events {
		r.Add(ev)
	}
	if r.Text != "fixed" || r.Steps != 2 || r.PromptTokens != 10 || r.CompletionTokens != 4 {
		t.Fatalf("%+v", r)
	}
	if r.ToolCalls != 2 || r.EditAttempts != 2 || r.EditFailures != 1 {
		t.Fatalf("edits %+v", r)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.EditFailures != 1 || got.Text != "fixed" {
		t.Fatalf("%+v", got)
	}
	buf.Reset()
	if err := WriteJSONL(&buf, events[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"kind":"step"`) || !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("jsonl %q", buf.String())
	}
}
