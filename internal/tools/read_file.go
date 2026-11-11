package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	defaultReadLimit = 2000
	maxReadBytes     = 256 * 1024
	maxLineBytes     = 1024 * 1024
)

type readFile struct{}

func (readFile) Spec() Spec {
	return Spec{
		Name:     "read_file",
		ReadOnly: true,
		Description: "Read a UTF-8 text file. Each line is printed as LINE|CONTENT. " +
			"The line number and pipe are not part of the file. " +
			"offset is the 1-based first line. limit is the maximum number of lines.",
		Parameters: objectSchema([]string{"path"}, map[string]any{
			"path":   strProp("Path relative to the workspace, or an absolute path inside it"),
			"offset": intProp("1-based starting line (default 1)"),
			"limit":  intProp("Maximum number of lines to return (default 2000)"),
		}),
	}
}

func (readFile) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	abs, err := resolvePath(env.Workspace, args.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_dir", args.Path)
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	var b strings.Builder
	lineNo := 0
	shown := 0
	truncated := false
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		lineNo++
		if lineNo < offset {
			continue
		}
		if shown >= limit {
			truncated = true
			break
		}
		line := fmt.Sprintf("%6d|%s\n", lineNo, sc.Text())
		if b.Len()+len(line) > maxReadBytes && shown > 0 {
			truncated = true
			break
		}
		b.WriteString(line)
		shown++
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if lineNo == 0 && shown == 0 && !truncated {
		return "(empty file)", nil
	}
	if shown == 0 && lineNo < offset {
		return "", fmt.Errorf("offset %d is past the end of the file (%d lines)", offset, lineNo)
	}
	if truncated {
		fmt.Fprintf(&b, "...[truncated; showing lines %d-%d. Raise offset to continue]\n", offset, offset+shown-1)
	}
	return b.String(), nil
}
