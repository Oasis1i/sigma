package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolvePath returns an absolute path that stays inside the workspace.
// Symlink targets are checked too: a link to a directory outside the
// workspace must not become a way to read or write that directory.
func resolvePath(workspace, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	workspace = filepath.Clean(workspace)
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(workspace, p))
	}
	if err := ensureInside(workspace, abs); err != nil {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("workspace: %w", err)
	}
	existing := abs
	var suffix []string
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	final := realExisting
	for _, part := range suffix {
		final = filepath.Join(final, part)
	}
	if err := ensureInside(realWS, final); err != nil {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	return abs, nil
}

// displayPath prefers a workspace-relative path in tool results.
func displayPath(workspace, abs string) string {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == "." {
		return abs
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return rel
}

func ensureInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("escapes the workspace")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("escapes the workspace")
	}
	return nil
}
