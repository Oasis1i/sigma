// Package prompt builds the system message, including AGENTS.md files.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Oasis1i/sigma/internal/config"
)

// maxAgentsBytes keeps a huge AGENTS.md from crowding out the conversation.
const maxAgentsBytes = 32 * 1024

// Section is one instruction file that was found.
type Section struct {
	Source string
	Body   string
}

// Build returns the system prompt for one run.
func Build(workspace, mode string, sections []Section) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are Sigma, a coding agent working in a local workspace. Complete the user's task by calling tools. When the task is done, reply with a short summary and do not call any tools.

Workspace: %s
Approval mode: %s

Tools:
- read_file reads a UTF-8 text file. Lines look like "    12|content". The line number and the pipe are not part of the file. offset is a 1-based line number; limit is how many lines to return.
- list_dir lists a single directory. It is not recursive. Directories end with /.
- grep searches file contents with a regular expression. It skips .git, symlinks, and files ignored by .gitignore.
- edit_file replaces old_string with new_string. old_string must match exactly once, including whitespace and line endings. If it matches 0 times or more than once, the edit is rejected and nothing is written. Prefer edit_file over write_file for existing files.
- write_file creates or overwrites a file and creates parent directories.
- bash runs a shell command in the workspace. stdout and stderr are combined, and the result includes the exit code.

Guidelines:
- Read a file before editing it. Copy old_string from the text after the pipe, not from the line-number prefix.
- Keep edits small. Do not revert changes you were not asked to touch.
- Use bash for tests and builds. Do not run destructive commands (rm -rf, git reset --hard, rewriting secrets).
- If a tool returns an error, read the message and try again with a correction.
- Prefer the native tool-calling interface. If you do not call a tool, a fenced bash block in your reply is executed as a bash command, so do not put example commands in bash fences unless you want them to run.
`, workspace, mode)

	switch mode {
	case "plan":
		b.WriteString("Plan mode is read-only. edit_file, write_file, and bash will be denied. Investigate and write a plan. Do not claim you changed files.\n")
	case "ask":
		b.WriteString("Mutating tools may wait for the user to approve them before they run.\n")
	case "auto":
		b.WriteString("Mutating tools run when the approval rules allow them.\n")
	}

	for _, s := range sections {
		body := strings.TrimSpace(s.Body)
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "\n# Instructions from %s\n\n%s\n", s.Source, body)
	}
	return b.String()
}

// LoadAgents reads the user-level AGENTS.md and the one at the git root
// (or the workspace, when there is no .git). A missing file is skipped.
// The same file is not included twice.
func LoadAgents(workspace string) ([]Section, error) {
	var sections []Section
	seen := map[string]bool{}
	add := func(path string) error {
		if path == "" {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			return nil
		}
		body, ok, err := readLimited(abs)
		if err != nil || !ok {
			return err
		}
		seen[abs] = true
		sections = append(sections, Section{Source: abs, Body: body})
		return nil
	}
	if err := add(config.UserAgentsPath()); err != nil {
		return nil, err
	}
	root := findRepoRoot(workspace)
	if err := add(filepath.Join(root, "AGENTS.md")); err != nil {
		return nil, err
	}
	if root != workspace {
		if err := add(filepath.Join(workspace, "AGENTS.md")); err != nil {
			return nil, err
		}
	}
	return sections, nil
}

func readLimited(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) > maxAgentsBytes {
		b = b[:maxAgentsBytes]
		return string(b) + "\n...[truncated]\n", true, nil
	}
	return string(b), true, nil
}

// findRepoRoot walks up looking for a .git entry. Worktrees use a .git file,
// so any non-missing .git counts. When none exists, the workspace is the root.
func findRepoRoot(start string) string {
	dir := start
	for {
		git := filepath.Join(dir, ".git")
		if _, err := os.Stat(git); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}
