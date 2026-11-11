package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/sandbox"
)

func TestBashExitCodeAndDirectory(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Workspace: dir, Runner: &sandbox.Local{Dir: dir}, BashTimeout: 5 * time.Second}
	tool := bashTool{}
	ctx := context.Background()

	out, err := tool.Execute(ctx, env, json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Clean(dir)) || !strings.Contains(out, "exit code: 0") {
		t.Fatalf("pwd:\n%s", out)
	}
	out, err = tool.Execute(ctx, env, json.RawMessage(`{"command":"echo err >&2; exit 3"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "err") || !strings.Contains(out, "exit code: 3") {
		t.Fatalf("stderr:\n%s", out)
	}
	if _, err := tool.Execute(ctx, env, json.RawMessage(`{"command":"   "}`)); err == nil {
		t.Fatal("expected empty command error")
	}
}

func TestBashTimeoutKillsChildren(t *testing.T) {
	dir := t.TempDir()
	env := &Env{
		Workspace:   dir,
		Runner:      &sandbox.Local{Dir: dir},
		BashTimeout: 400 * time.Millisecond,
	}
	tool := bashTool{}
	for _, cmd := range []string{"sleep 30", "sleep 30 & wait"} {
		t.Run(cmd, func(t *testing.T) {
			start := time.Now()
			out, err := tool.Execute(context.Background(), env, json.RawMessage(`{"command":"`+cmd+`"}`))
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if elapsed > 8*time.Second {
				t.Fatalf("timeout did not return, took %s", elapsed)
			}
			if !strings.Contains(out, "timed out") || !strings.Contains(out, "exit code: 124") {
				t.Fatalf("output:\n%s", out)
			}
		})
	}
}

func TestBashTruncatesHeadAndTail(t *testing.T) {
	dir := t.TempDir()
	env := &Env{
		Workspace:   dir,
		Runner:      &sandbox.Local{Dir: dir, HeadBytes: 32, TailBytes: 32},
		BashTimeout: 5 * time.Second,
	}
	cmd := `printf '%400s' '' | tr ' ' A; printf '%400s' '' | tr ' ' B`
	out, err := (bashTool{}).Execute(context.Background(), env, json.RawMessage(`{"command":`+mustJSON(cmd)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bytes truncated") {
		t.Fatalf("missing truncation marker:\n%s", out)
	}
	head := out
	if i := strings.Index(out, "bytes truncated"); i >= 0 {
		head = out[:i]
	}
	if !strings.Contains(head, "AAA") {
		t.Fatalf("head lost the start of the output:\n%s", out)
	}
	tail := out
	if i := strings.Index(out, "bytes truncated"); i >= 0 {
		tail = out[i:]
	}
	if !strings.Contains(tail, "BBB") {
		t.Fatalf("tail lost the end of the output:\n%s", out)
	}
	if !strings.Contains(out, "exit code: 0") {
		t.Fatalf("missing exit code:\n%s", out)
	}
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
