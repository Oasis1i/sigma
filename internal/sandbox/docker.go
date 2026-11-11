package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Docker runs bash inside a container. It is a Runner, the same interface
// Local implements, so the approval policy does not change when the user
// sets sandbox = "docker". The policy still decides whether the command may
// run. This runner only decides where.
//
// The workspace is mounted at /workspace. The container runs as the current
// uid so files it creates stay editable. The image must provide bash.
type Docker struct {
	Image string
	Dir   string
}

// RunArgs is the docker CLI invocation for one command, without "docker"
// itself. Tests check this list instead of requiring a daemon.
func (d *Docker) RunArgs(name, command string) []string {
	args := []string{
		"run", "--rm", "--name", name,
		"-v", d.Dir + ":/workspace",
		"-w", "/workspace",
	}
	if uid := os.Getuid(); uid >= 0 {
		args = append(args, "-u", fmt.Sprintf("%d:%d", uid, os.Getgid()))
	}
	args = append(args, d.Image, "bash", "-c", command)
	return args
}

// Run executes command in the configured image. On timeout or cancel the
// container is killed by name; --rm removes it when it stops.
func (d *Docker) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("command is required")
	}
	if strings.TrimSpace(d.Image) == "" {
		return Result{}, fmt.Errorf("docker image is not set")
	}
	if d.Dir == "" {
		return Result{}, fmt.Errorf("sandbox directory is not set")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return Result{}, fmt.Errorf("docker is not installed")
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	name := fmt.Sprintf("sigma-%d", time.Now().UnixNano())

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", d.RunArgs(name, command)...)
	cmd.Stdin = nil
	buf := newCapBuffer(DefaultHeadBytes, DefaultTailBytes)
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.Cancel = func() error {
		kill := exec.Command("docker", "kill", name)
		_ = kill.Run()
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	err := cmd.Run()
	result := Result{Output: buf.String()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = 124
		return result, nil
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			result.ExitCode = ee.ExitCode()
			return result, nil
		}
		return result, fmt.Errorf("docker: %w", err)
	}
	result.ExitCode = 0
	return result, nil
}
