// Package checkpoint stores workspace snapshots as git commits that are not
// on the user's branch and do not use the user's index.
//
// Each snapshot is an ordinary commit object referenced from
// refs/sigma/<session>/<n>. Creating it uses a temporary index (GIT_INDEX_FILE),
// so `git status`, HEAD, and the staged diff stay as the user left them.
// /undo checks that commit back out through the same temporary index and
// deletes files the snapshot does not contain. Outside a git repository the
// store reports itself disabled and does nothing.
package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrUnavailable is returned when the workspace is not a git checkout or git
// is not installed. Callers turn it into a notice rather than a failed turn.
var ErrUnavailable = errors.New("git checkpoint is unavailable")

// Store is the checkpoint stack for one session in one workspace.
type Store struct {
	Dir     string
	Session string
}

// SetSession points later snapshots at a new session id. /clear and /resume
// change the open log; the refs follow that id so undo does not restore a
// different conversation's files.
func (s *Store) SetSession(id string) {
	if s != nil {
		s.Session = id
	}
}

// Enabled reports whether snapshot and undo can run here.
func (s *Store) Enabled(ctx context.Context) bool {
	if s == nil || strings.TrimSpace(s.Dir) == "" || !validSession(s.Session) {
		return false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	out, err := s.git(ctx, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Snapshot records the worktree, including untracked files that are not
// ignored. The returned ref is the new hidden ref.
func (s *Store) Snapshot(ctx context.Context) (string, error) {
	if !s.Enabled(ctx) {
		return "", ErrUnavailable
	}
	index, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer os.Remove(index)
	env := s.indexEnv(index)
	if s.hasHead(ctx) {
		if _, err := s.git(ctx, env, "read-tree", "HEAD"); err != nil {
			return "", fmt.Errorf("checkpoint read-tree: %w", err)
		}
	}
	if _, err := s.git(ctx, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("checkpoint add: %w", err)
	}
	tree, err := s.git(ctx, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("checkpoint write-tree: %w", err)
	}
	tree = bytes.TrimSpace(tree)
	args := []string{"commit-tree", string(tree), "-m", "sigma checkpoint"}
	if prev, _ := s.latest(ctx); prev != "" {
		args = append(args, "-p", prev)
	}
	commit, err := s.git(ctx, s.identEnv(), args...)
	if err != nil {
		return "", fmt.Errorf("checkpoint commit: %w", err)
	}
	commit = bytes.TrimSpace(commit)
	n := s.next(ctx)
	ref := fmt.Sprintf("refs/sigma/%s/%d", s.Session, n)
	if _, err := s.git(ctx, nil, "update-ref", ref, string(commit)); err != nil {
		return "", fmt.Errorf("checkpoint update-ref: %w", err)
	}
	return ref, nil
}

// Undo restores the newest snapshot into the worktree and drops that ref, so
// a second undo restores the one before it. HEAD and the index are not moved.
func (s *Store) Undo(ctx context.Context) (string, error) {
	if !s.Enabled(ctx) {
		return "", ErrUnavailable
	}
	refs, err := s.refs(ctx)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", fmt.Errorf("no checkpoint to undo")
	}
	ref := refs[len(refs)-1]
	commit, err := s.git(ctx, nil, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("undo rev-parse: %w", err)
	}
	commit = bytes.TrimSpace(commit)
	if err := s.restore(ctx, string(commit)); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, nil, "update-ref", "-d", ref); err != nil {
		return "", fmt.Errorf("undo delete ref: %w", err)
	}
	return ref, nil
}

func (s *Store) restore(ctx context.Context, commit string) error {
	index, err := tempIndex()
	if err != nil {
		return err
	}
	defer os.Remove(index)
	env := s.indexEnv(index)
	if _, err := s.git(ctx, env, "read-tree", commit); err != nil {
		return fmt.Errorf("undo read-tree: %w", err)
	}
	// checkout-index writes the temporary index into the worktree. Because
	// GIT_INDEX_FILE points at our file, the user's index is not updated.
	if _, err := s.git(ctx, env, "checkout-index", "-a", "-f"); err != nil {
		return fmt.Errorf("undo checkout: %w", err)
	}
	return s.removeExtra(ctx, commit)
}

func (s *Store) removeExtra(ctx context.Context, commit string) error {
	out, err := s.git(ctx, nil, "ls-tree", "-r", "--name-only", "-z", commit)
	if err != nil {
		return fmt.Errorf("undo list tree: %w", err)
	}
	keep := map[string]bool{}
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		keep[string(name)] = true
	}
	return filepath.WalkDir(s.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.Dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if keep[rel] || s.ignored(ctx, rel) {
			return nil
		}
		return os.Remove(path)
	})
}

func (s *Store) ignored(ctx context.Context, rel string) bool {
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "-q", "--", rel)
	cmd.Dir = s.Dir
	err := cmd.Run()
	return err == nil
}

func (s *Store) hasHead(ctx context.Context) bool {
	_, err := s.git(ctx, nil, "rev-parse", "--verify", "HEAD")
	return err == nil
}

func (s *Store) latest(ctx context.Context) (string, error) {
	refs, err := s.refs(ctx)
	if err != nil || len(refs) == 0 {
		return "", err
	}
	out, err := s.git(ctx, nil, "rev-parse", refs[len(refs)-1])
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out)), nil
}

func (s *Store) refs(ctx context.Context) ([]string, error) {
	prefix := "refs/sigma/" + s.Session + "/"
	out, err := s.git(ctx, nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		// No refs yet is an empty list, not a failure. for-each-ref exits 0
		// even when nothing matches; any error here is a real git problem.
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			refs = append(refs, line)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		return refNumber(refs[i]) < refNumber(refs[j])
	})
	return refs, nil
}

func (s *Store) next(ctx context.Context) int {
	refs, _ := s.refs(ctx)
	n := 0
	for _, ref := range refs {
		if v := refNumber(ref); v > n {
			n = v
		}
	}
	return n + 1
}

func refNumber(ref string) int {
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(ref[i+1:])
	return n
}

func (s *Store) git(ctx context.Context, extra []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.Dir
	if extra != nil {
		cmd.Env = extra
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (s *Store) indexEnv(index string) []string {
	return append(os.Environ(), "GIT_INDEX_FILE="+index)
}

// identEnv gives commit-tree an author without writing to the user's git
// config. The name is not used as a branch committer the user will see.
func (s *Store) identEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=sigma",
		"GIT_AUTHOR_EMAIL=sigma@localhost",
		"GIT_COMMITTER_NAME=sigma",
		"GIT_COMMITTER_EMAIL=sigma@localhost",
	)
}

func tempIndex() (string, error) {
	f, err := os.CreateTemp("", "sigma-index-")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	// An empty file is not a valid index. Remove it and let git create one.
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func validSession(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
