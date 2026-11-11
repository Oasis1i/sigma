package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type editFile struct{}

func (editFile) Spec() Spec {
	return Spec{
		Name: "edit_file",
		Description: "Replace old_string with new_string in a file. old_string must occur exactly once " +
			"and must match the file bytes, including whitespace and line endings. " +
			"0 matches or more than 1 match is an error and the file is not changed. " +
			"To create a file, use write_file. new_string may be empty to delete the matched text.",
		Parameters: objectSchema([]string{"path", "old_string", "new_string"}, map[string]any{
			"path":       strProp("File to edit, relative to the workspace"),
			"old_string": strProp("Exact text to replace. Include surrounding lines so the match is unique"),
			"new_string": strProp("Replacement text. Empty deletes the matched text"),
		}),
	}
}

func (editFile) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Path      string  `json:"path"`
		OldString string  `json:"old_string"`
		NewString *string `json:"new_string"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.NewString == nil {
		return "", fmt.Errorf("new_string is required")
	}
	if args.OldString == "" {
		return "", fmt.Errorf("old_string must not be empty; use write_file to create a file")
	}
	abs, err := resolvePath(env.Workspace, args.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file does not exist: %s (use write_file to create it)", args.Path)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", args.Path)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := string(data)
	count := strings.Count(content, args.OldString)
	if count != 1 {
		return "", fmt.Errorf("%s", editCountError(args.Path, content, args.OldString, count))
	}
	if args.OldString == *args.NewString {
		return fmt.Sprintf("unchanged %s (old_string and new_string are identical)", displayPath(env.Workspace, abs)), nil
	}
	updated := strings.Replace(content, args.OldString, *args.NewString, 1)
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(abs, []byte(updated), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (1 replacement)", displayPath(env.Workspace, abs)), nil
}

func editCountError(path, content, old string, count int) string {
	if count == 0 {
		hint := ""
		normFile := strings.ReplaceAll(content, "\r\n", "\n")
		normOld := strings.ReplaceAll(old, "\r\n", "\n")
		if normOld != "" && strings.Contains(normFile, normOld) && (strings.Contains(content, "\r\n") || strings.Contains(old, "\r\n")) {
			hint = "; line endings differ (CRLF vs LF). Copy the exact bytes from read_file"
		}
		return fmt.Sprintf("old_string matched 0 times in %s%s", path, hint)
	}
	return fmt.Sprintf("old_string matched %d times in %s; include more surrounding context so the match is unique", count, path)
}
