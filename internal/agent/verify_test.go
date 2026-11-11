package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
)

func TestVerifyFeedsFailureBack(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"hello.txt","content":"no\n"}`,
		}}},
		{ToolCalls: []provider.ToolCall{{
			ID: "c2", Name: "write_file", Arguments: `{"path":"hello.txt","content":"hi\n"}`,
		}}},
		{Content: "fixed"},
	}}
	ag, dir := newTestAgent(t, "auto", nil, script)
	ag.Verify = []string{"grep -q '^hi$' hello.txt"}
	ag.VerifyRunner = &sandbox.Local{Dir: dir}
	if err := ag.RunTurn(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi\n" {
		t.Fatalf("file %q", body)
	}
	saw := false
	for _, batch := range script.Seen {
		for _, m := range batch {
			if m.Role == "user" && strings.Contains(m.Content, "Verification failed") {
				saw = true
			}
		}
	}
	if !saw {
		t.Fatal("model was not shown the verify failure")
	}
}

func TestVerifySkipsPlainReplies(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{{Content: "nothing to change"}}}
	ag, dir := newTestAgent(t, "auto", nil, script)
	ag.Verify = []string{"false"}
	ag.VerifyRunner = &sandbox.Local{Dir: dir}
	if err := ag.RunTurn(context.Background(), "look around"); err != nil {
		t.Fatal(err)
	}
	if len(script.Seen) != 1 {
		t.Fatalf("verify ran without an edit: %d calls", len(script.Seen))
	}
}
