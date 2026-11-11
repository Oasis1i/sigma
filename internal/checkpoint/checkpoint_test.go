package checkpoint

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnavailableOutsideGit(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Session: "sess"}
	if s.Enabled(context.Background()) {
		t.Fatal("temp dir is not a git repository")
	}
	_, err := s.Undo(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestSnapshotAndUndoLeaveBranchAndIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=sigma-test",
			"GIT_AUTHOR_EMAIL=sigma-test@localhost",
			"GIT_COMMITTER_NAME=sigma-test",
			"GIT_COMMITTER_EMAIL=sigma-test@localhost",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s", strings.Join(args, " "), out)
		}
		return string(out)
	}
	git("init", "-b", "main")
	readme := filepath.Join(dir, "README")
	if err := os.WriteFile(readme, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-m", "init")
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	branch := strings.TrimSpace(git("branch", "--show-current"))

	if err := os.WriteFile(readme, []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	cached := git("diff", "--cached")
	if err := os.WriteFile(readme, []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Store{Dir: dir, Session: "sess-1"}
	ref, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "refs/sigma/sess-1/") {
		t.Fatalf("ref %s", ref)
	}

	if err := os.WriteFile(readme, []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}

	restored, err := s.Undo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restored != ref {
		t.Fatalf("restored %s want %s", restored, ref)
	}
	got, err := os.ReadFile(readme)
	if err != nil || string(got) != "work\n" {
		t.Fatalf("readme %q %v", got, err)
	}
	notes, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil || string(notes) != "hello\n" {
		t.Fatalf("notes %q %v", notes, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("undo left a file created after the snapshot")
	}
	if strings.TrimSpace(git("rev-parse", "HEAD")) != head {
		t.Fatal("HEAD moved")
	}
	if strings.TrimSpace(git("branch", "--show-current")) != branch {
		t.Fatal("branch changed")
	}
	if git("diff", "--cached") != cached {
		t.Fatalf("index changed:\n%s\nwant:\n%s", git("diff", "--cached"), cached)
	}

	if _, err := s.Undo(context.Background()); err == nil {
		t.Fatal("second undo should fail with no checkpoint left")
	}
}
