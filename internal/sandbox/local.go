package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultHeadBytes and DefaultTailBytes are the captured ends of a command.
	// The middle is dropped so a noisy test suite cannot fill the context.
	DefaultHeadBytes = 16 * 1024
	DefaultTailBytes = 16 * 1024
	defaultTimeout   = 120 * time.Second
)

// Local runs commands on this machine. Dir is the working directory.
// HeadBytes and TailBytes cap captured output; zero selects the default.
type Local struct {
	Dir       string
	HeadBytes int
	TailBytes int
}

// Run starts bash -c in Dir. The parent's environment is inherited, including
// PATH, but bash is not a login shell: profile scripts often cd or alias
// commands, which makes tool results harder to predict.
func (l *Local) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("command is required")
	}
	if l.Dir == "" {
		return Result{}, fmt.Errorf("sandbox directory is not set")
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", command)
	cmd.Dir = l.Dir
	cmd.Env = withPWD(os.Environ(), l.Dir)
	cmd.Stdin = nil

	head, tail := l.HeadBytes, l.TailBytes
	if head <= 0 {
		head = DefaultHeadBytes
	}
	if tail <= 0 {
		tail = DefaultTailBytes
	}
	buf := newCapBuffer(head, tail)
	cmd.Stdout = buf
	cmd.Stderr = buf

	// Setpgid makes the shell a group leader so timeout can kill children
	// (sleep started by the script) and not only bash itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			_ = cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
	result := Result{Output: buf.String()}

	// A cancelled parent is not a tool timeout. The agent stops the turn.
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
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
		return result, fmt.Errorf("run command: %w", err)
	}
	result.ExitCode = 0
	return result, nil
}

func withPWD(base []string, dir string) []string {
	env := make([]string, 0, len(base)+1)
	for _, e := range base {
		if strings.HasPrefix(e, "PWD=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "PWD="+dir)
}
