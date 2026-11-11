package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitignoreSubset(t *testing.T) {
	dir := t.TempDir()
	body := "*.log\n/only-root.txt\nsecret/\n!keep.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g := newGitignore()
	g.load(dir, "")
	cases := []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"a.log", false, true},
		{"sub/a.log", false, true},
		{"keep.log", false, false},
		{"only-root.txt", false, true},
		{"sub/only-root.txt", false, false},
		{"secret", true, true},
		{"secret.txt", false, false},
		{"main.go", false, false},
	}
	for _, tc := range cases {
		if got := g.ignored(tc.rel, tc.dir); got != tc.want {
			t.Errorf("ignored(%q, dir=%v) = %v, want %v", tc.rel, tc.dir, got, tc.want)
		}
	}
}

func TestGrepRespectsGitignoreAndCaps(t *testing.T) {
	dir := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, ".gitignore"), "*.log\n")
	write(filepath.Join(dir, "main.go"), "package main\nfunc Alpha() {}\n")
	write(filepath.Join(dir, "debug.log"), "func Alpha() {}\n")
	write(filepath.Join(dir, "sub", "b.go"), "package sub\nfunc Alpha() {}\n")
	write(filepath.Join(dir, "sub", ".gitignore"), "skip.go\n")
	write(filepath.Join(dir, "sub", "skip.go"), "func Alpha() {}\n")
	write(filepath.Join(dir, ".git", "config"), "func Alpha() {}\n")

	out, err := (grepTool{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"pattern":"func Alpha","glob":"*.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go:") || !strings.Contains(out, "sub/b.go:") {
		t.Fatalf("missing sources:\n%s", out)
	}
	if strings.Contains(out, "debug.log") || strings.Contains(out, "skip.go") || strings.Contains(out, ".git") {
		t.Fatalf("ignored file leaked:\n%s", out)
	}

	var b strings.Builder
	for i := 0; i < maxGrepMatches+10; i++ {
		b.WriteString("hit\n")
	}
	write(filepath.Join(dir, "many.txt"), b.String())
	out, err = (grepTool{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"pattern":"hit","path":"many.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected cap:\n%s", out)
	}
	if _, err := (grepTool{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"pattern":"("}`)); err == nil {
		t.Fatal("expected bad regex")
	}
}
