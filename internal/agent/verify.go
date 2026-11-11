package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Oasis1i/sigma/internal/provider"
)

// verify runs the configured commands after a successful edit. A non-zero
// exit is appended as a user message so the next model call can fix it.
// Canceling the turn stops the commands and returns the context error.
func (a *Agent) verify(ctx context.Context) error {
	if a == nil || len(a.Verify) == 0 {
		return nil
	}
	if a.VerifyRunner == nil {
		a.notice("verify skipped: no command runner")
		return nil
	}
	var failed []string
	for _, cmd := range a.Verify {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := a.VerifyRunner.Run(ctx, cmd, a.verifyTimeout())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed = append(failed, fmt.Sprintf("$ %s\n%s", cmd, err.Error()))
			continue
		}
		if res.ExitCode != 0 || res.TimedOut {
			out := strings.TrimSpace(res.Output)
			if out == "" {
				out = fmt.Sprintf("exit code %d", res.ExitCode)
			}
			failed = append(failed, fmt.Sprintf("$ %s\n%s", cmd, out))
		}
	}
	if len(failed) == 0 {
		a.notice("verify passed")
		return nil
	}
	a.notice("verify failed")
	body := "Verification failed. These commands ran after your edits. Fix the cause before finishing.\n\n" +
		strings.Join(failed, "\n\n")
	return a.append(provider.Message{Role: "user", Content: body})
}

func (a *Agent) verifyTimeout() time.Duration {
	if a != nil && a.VerifyTimeout > 0 {
		return a.VerifyTimeout
	}
	return 2 * time.Minute
}
