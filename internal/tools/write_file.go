package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type writeFile struct{}

func (writeFile) Spec() Spec {
	return Spec{
		Name: "write_file",
		Description: "Create or overwrite a file with the given content. Parent directories are created. " +
			"Prefer edit_file when changing part of an existing file.",
		Parameters: objectSchema([]string{"path", "content"}, map[string]any{
			"path":    strProp("File to write, relative to the workspace"),
			"content": strProp("Full new contents of the file. May be empty"),
		}),
	}
}

func (writeFile) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Content == nil {
		return "", fmt.Errorf("content is required")
	}
	abs, err := resolvePath(env.Workspace, args.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(*args.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d bytes)", displayPath(env.Workspace, abs), len(*args.Content)), nil
}
