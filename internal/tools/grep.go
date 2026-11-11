package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxGrepMatches   = 100
	maxGrepFileBytes = 1 << 20
	maxGrepLine      = 500
)

type grepTool struct{}

func (grepTool) Spec() Spec {
	return Spec{
		Name:     "grep",
		ReadOnly: true,
		Description: "Search files with a Go regular expression. Skips .git, symlinks, binary files, " +
			"files larger than 1MB, and paths ignored by .gitignore. " +
			"Results look like path:line:text and are capped.",
		Parameters: objectSchema([]string{"pattern"}, map[string]any{
			"pattern": strProp("Go regular expression"),
			"path":    strProp("File or directory to search. Defaults to the workspace root"),
			"glob":    strProp("Optional glob matched against the file name, for example *.go"),
		}),
	}
}

func (g grepTool) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}
	if args.Glob != "" {
		if _, err := filepath.Match(args.Glob, "x"); err != nil {
			return "", fmt.Errorf("invalid glob: %w", err)
		}
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	abs, err := resolvePath(env.Workspace, args.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}

	ig := newGitignore()
	ig.load(env.Workspace, "")
	if info.IsDir() {
		// Parent .gitignore files still apply when the search starts below the root.
		preloadGitignores(ig, env.Workspace, abs)
	}

	var matches []string
	skippedLarge := 0
	truncated := false
	searchFile := func(filePath, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if args.Glob != "" && !globMatch(args.Glob, rel) {
			return nil
		}
		st, err := os.Stat(filePath)
		if err != nil {
			return nil
		}
		if st.Size() > maxGrepFileBytes {
			skippedLarge++
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		if isBinary(data) {
			return nil
		}
		text := strings.TrimSuffix(string(data), "\n")
		if text == "" {
			return nil
		}
		for i, line := range strings.Split(text, "\n") {
			if re.MatchString(line) {
				if len(line) > maxGrepLine {
					line = line[:maxGrepLine] + "..."
				}
				matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
				if len(matches) >= maxGrepMatches {
					truncated = true
					return errStop
				}
			}
		}
		return nil
	}

	if !info.IsDir() {
		rel, err := filepath.Rel(env.Workspace, abs)
		if err != nil {
			rel = abs
		}
		if err := searchFile(abs, filepath.ToSlash(rel)); err != nil && err != errStop {
			return "", err
		}
	} else {
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == abs {
				return nil
			}
			rel, relErr := filepath.Rel(env.Workspace, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			name := d.Name()
			if d.IsDir() {
				if name == ".git" || ig.ignored(rel, true) {
					return filepath.SkipDir
				}
				ig.load(path, rel)
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if ig.ignored(rel, false) {
				return nil
			}
			return searchFile(path, rel)
		})
		if err != nil && err != errStop {
			return "", err
		}
	}

	var b strings.Builder
	if len(matches) == 0 {
		b.WriteString("no matches\n")
	} else {
		if truncated {
			fmt.Fprintf(&b, "%d matches (truncated)\n", len(matches))
		} else {
			fmt.Fprintf(&b, "%d matches\n", len(matches))
		}
		for _, m := range matches {
			b.WriteString(m)
			b.WriteByte('\n')
		}
	}
	if skippedLarge > 0 {
		fmt.Fprintf(&b, "skipped %d files larger than 1MB\n", skippedLarge)
	}
	return b.String(), nil
}

var errStop = fmt.Errorf("stop")

func preloadGitignores(ig *gitignore, workspace, searchAbs string) {
	rel, err := filepath.Rel(workspace, searchAbs)
	if err != nil || rel == "." {
		return
	}
	cur := workspace
	var relParts []string
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, part)
		relParts = append(relParts, part)
		if cur == searchAbs {
			info, err := os.Stat(cur)
			if err == nil && info.IsDir() {
				ig.load(cur, strings.Join(relParts, "/"))
			}
			return
		}
		ig.load(cur, strings.Join(relParts, "/"))
	}
}

func globMatch(pattern, rel string) bool {
	name := rel
	if strings.Contains(pattern, "/") {
		ok, err := pathMatch(pattern, rel)
		return err == nil && ok
	}
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		name = rel[i+1:]
	}
	ok, err := pathMatch(pattern, name)
	return err == nil && ok
}

func pathMatch(pattern, name string) (bool, error) {
	return filepath.Match(pattern, name)
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
