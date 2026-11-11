package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type bashTool struct{}

func (bashTool) Spec() Spec {
	return Spec{
		Name: "bash",
		Description: "Run a bash command in the workspace directory. stdout and stderr are combined. " +
			"The result ends with the exit code. Very large output keeps its head and tail and drops the middle. " +
			"timeout is a shorter limit in seconds; it cannot exceed the configured timeout.",
		Parameters: objectSchema([]string{"command"}, map[string]any{
			"command": strProp("Bash command to run"),
			"timeout": intProp("Optional timeout in seconds, capped by the configured timeout"),
		}),
	}
}

func (bashTool) Execute(ctx context.Context, env *Env, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if env.Runner == nil {
		return "", fmt.Errorf("bash is not available: no command runner is configured")
	}
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	timeout := env.BashTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if args.Timeout > 0 {
		req := time.Duration(args.Timeout) * time.Second
		if req < timeout {
			timeout = req
		}
	}
	res, err := env.Runner.Run(ctx, args.Command, timeout)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if res.Output != "" {
		b.WriteString(res.Output)
		if !strings.HasSuffix(res.Output, "\n") {
			b.WriteByte('\n')
		}
	}
	if res.TimedOut {
		fmt.Fprintf(&b, "exit code: %d (timed out after %s)\n", res.ExitCode, timeout)
	} else {
		fmt.Fprintf(&b, "exit code: %d\n", res.ExitCode)
	}
	return b.String(), nil
}
