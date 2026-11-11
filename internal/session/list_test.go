package session

import (
	"os"
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
