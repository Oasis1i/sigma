package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oasis1i/sigma/internal/checkpoint"
	"github.com/Oasis1i/sigma/internal/provider"
)

func TestUndoOutsideGit(t *testing.T) {
	eng, _ := newEngine(t, "auto", &provider.Scripted{})
	eng.cp = &checkpoint.Store{Dir: eng.workspace, Session: eng.SessionID()}
	effect, err := eng.ApplySlash("/undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(effect.Notice, "unavailable") {
		t.Fatalf("notice %q", effect.Notice)
	}
}

func TestUndoRestoresFilesFromCheckpoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"hello.txt","content":"hi\n"}`,
		}}},
		{Content: "wrote it"},
	}}
	eng, ws := newEngine(t, "auto", script)
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=sigma-test",
		"GIT_AUTHOR_EMAIL=sigma-test@localhost",
		"GIT_COMMITTER_NAME=sigma-test",
		"GIT_COMMITTER_EMAIL=sigma-test@localhost",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	eng.cp = &checkpoint.Store{Dir: ws, Session: eng.SessionID()}
	eng.Submit(Command{Kind: CmdMessage, Text: "write hello"})
	evs := collect(t, eng, nil)
	if _, err := os.Stat(filepath.Join(ws, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	saw := false
	for _, ev := range evs {
		if ev.Kind == KindNotice && strings.Contains(ev.Text, "checkpoint refs/sigma/") {
			saw = true
		}
	}
	if !saw {
		t.Fatal("turn did not record a checkpoint")
	}
	effect, err := eng.ApplySlash("/undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(effect.Notice, "restored") {
		t.Fatalf("notice %q", effect.Notice)
	}
	if _, err := os.Stat(filepath.Join(ws, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("undo left the file the turn created")
	}
}
