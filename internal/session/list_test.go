package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/provider"
)

func TestListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	older, err := Create(dir, map[string]string{"model": "old-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := older.AppendMessage(provider.Message{Role: "user", Content: "first task"}); err != nil {
		t.Fatal(err)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}
	newer, err := Create(dir, map[string]string{"model": "new-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := newer.AppendMessage(provider.Message{Role: "user", Content: "second task\nwith detail"}); err != nil {
		t.Fatal(err)
	}
	if err := newer.Close(); err != nil {
		t.Fatal(err)
	}
	// List orders by mtime. Set the stamps explicitly so a coarse filesystem
	// clock cannot make the two files look simultaneous.
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older.Path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer.Path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len %d", len(list))
	}
	if list[0].ID != newer.ID || list[0].Model != "new-model" || list[0].Preview != "second task with detail" {
		t.Fatalf("newest %+v", list[0])
	}
	if list[1].ID != older.ID || list[1].Preview != "first task" {
		t.Fatalf("older %+v", list[1])
	}
	empty, err := List(dir + "-missing")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing dir: %v %v", empty, err)
	}
}

func TestPreviewSkipsModeNote(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir, map[string]string{"model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	long := "fix the failing test in add.go and explain why the helper returned the wrong sum for negative numbers"
	for _, m := range []provider.Message{
		{Role: "system", Content: "You are Sigma\nApproval mode: ask\n"},
		{Role: "user", Content: "Approval mode is now auto."},
		{Role: "user", Content: long},
	} {
		if err := s.AppendMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len %d", len(list))
	}
	if strings.Contains(list[0].Preview, "Approval mode") {
		t.Fatalf("title used the mode note: %q", list[0].Preview)
	}
	if !strings.HasPrefix(list[0].Preview, "fix the failing test") || !strings.HasSuffix(list[0].Preview, "...") {
		t.Fatalf("preview %q", list[0].Preview)
	}
	if !IsRealUserPrompt(long) || IsRealUserPrompt("Approval mode is now plan.") {
		t.Fatal("IsRealUserPrompt")
	}
}
