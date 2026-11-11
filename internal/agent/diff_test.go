package agent

import (
	"strings"
	"testing"
)

func TestToolDiffEdit(t *testing.T) {
	got := ToolDiff("edit_file", `{"path":"add.go","old_string":"return a - b\n","new_string":"return a + b\n"}`)
	if !strings.Contains(got, "--- a/add.go") || !strings.Contains(got, "+++ b/add.go") {
		t.Fatalf("headers:\n%s", got)
	}
	if !strings.Contains(got, "-return a - b") || !strings.Contains(got, "+return a + b") {
		t.Fatalf("body:\n%s", got)
	}
	if ToolDiff("bash", `{"command":"ls"}`) != "" {
		t.Fatal("bash should not have a diff")
	}
}

func TestSnippetDiffKeepsContext(t *testing.T) {
	got := snippetDiff("a.go", "a\nb\nc", "a\nx\nc")
	want := "--- a/a.go\n+++ b/a.go\n@@\n a\n-b\n+x\n c\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
