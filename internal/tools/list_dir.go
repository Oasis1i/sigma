package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxListEntries = 200

type listDir struct{}

func (listDir) Spec() Spec {
	return Spec{
		Name:     "list_dir",
		ReadOnly: true,
		Description: "List one directory, not recursively. Directories end with /. " +
			"Ignored files are included; use grep to search contents.",
		Parameters: objectSchema(nil, map[string]any{
			"path": strProp("Directory relative to the workspace. Defaults to the workspace root"),
		}),
	}
}

func (listDir) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Path string `json:"path"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
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
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file; use read_file", args.Path)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var b strings.Builder
	n := len(entries)
	if n > maxListEntries {
		n = maxListEntries
	}
	for i := 0; i < n; i++ {
		e := entries[i]
		name := e.Name()
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", name)
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filepath.Join(abs, name))
			if err != nil {
				fmt.Fprintf(&b, "%s@\n", name)
				continue
			}
			fmt.Fprintf(&b, "%s -> %s\n", name, target)
			continue
		}
		info, err := e.Info()
		if err != nil {
			fmt.Fprintf(&b, "%s\n", name)
			continue
		}
		fmt.Fprintf(&b, "%s  %d\n", name, info.Size())
	}
	if len(entries) > maxListEntries {
		fmt.Fprintf(&b, "...[%d more entries]\n", len(entries)-maxListEntries)
	}
	return b.String(), nil
}
