package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditUniqueness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha foo beta\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{Workspace: dir}
	tool := editFile{}
	ctx := context.Background()

	_, err := tool.Execute(ctx, env, json.RawMessage(`{"path":"a.txt","old_string":"missing","new_string":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "matched 0 times") {
		t.Fatalf("zero matches: %v", err)
	}
	_, err = tool.Execute(ctx, env, json.RawMessage(`{"path":"a.txt","old_string":"foo","new_string":"bar"}`))
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("two matches: %v", err)
	}
	// The failed edits must not have changed the file.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "alpha foo beta\nfoo\n" {
		t.Fatalf("file changed:\n%s", body)
	}

	out, err := tool.Execute(ctx, env, json.RawMessage(`{"path":"a.txt","old_string":"alpha foo beta","new_string":"alpha bar beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatal(out)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "alpha bar beta\nfoo\n" {
		t.Fatalf("got %q", body)
	}

	_, err = tool.Execute(ctx, env, json.RawMessage(`{"path":"a.txt","old_string":"","new_string":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty old: %v", err)
	}
	_, err = tool.Execute(ctx, env, json.RawMessage(`{"path":"missing.txt","old_string":"a","new_string":"b"}`))
	if err == nil || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("missing: %v", err)
	}
	_, err = tool.Execute(ctx, env, json.RawMessage(`{"path":"a.txt","old_string":"foo"}`))
	if err == nil || !strings.Contains(err.Error(), "new_string") {
		t.Fatalf("missing new: %v", err)
	}
}

func TestEditLineEndingHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (editFile{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"path":"crlf.txt","old_string":"a\nb","new_string":"a\nc"}`))
	if err == nil || !strings.Contains(err.Error(), "line endings") {
		t.Fatalf("got %v", err)
	}
}

func TestEditPathEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := (editFile{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"path":"../secret.txt","old_string":"hidden","new_string":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("relative escape: %v", err)
	}
	_, err = (editFile{}).Execute(context.Background(), &Env{Workspace: dir}, json.RawMessage(`{"path":"link/secret.txt","old_string":"hidden","new_string":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("symlink escape: %v", err)
	}
	body, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hidden" {
		t.Fatalf("outside file changed: %s", body)
	}
}

func TestWriteCreatesParentsAndReadNumbersLines(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Workspace: dir}
	ctx := context.Background()
	out, err := (writeFile{}).Execute(ctx, env, json.RawMessage(`{"path":"sub/dir/note.txt","content":"one\ntwo\nthree\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3 bytes") && !strings.Contains(out, "wrote") {
		t.Fatal(out)
	}
	got, err := (readFile{}).Execute(ctx, env, json.RawMessage(`{"path":"sub/dir/note.txt","offset":2,"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "2|two") || strings.Contains(got, "one") || strings.Contains(got, "three") {
		t.Fatalf("read window:\n%s", got)
	}
	listing, err := (listDir{}).Execute(ctx, env, json.RawMessage(`{"path":"sub/dir"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "note.txt") {
		t.Fatal(listing)
	}
}
