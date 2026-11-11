// Package sandbox is where a command runs, not whether it is allowed to.
//
// Approval lives in package approval. The agent checks approval first, then
// calls a Runner. Local executes on this machine, with the workspace as the
// working directory. A Docker runner can implement Runner later. File tools
// still read and write the local disk in this MVP; a remote sandbox would
// need a filesystem hook next to Runner.
package sandbox

import (
	"context"
	"time"
)

// Result is the captured output of one command.
type Result struct {
	Output   string
	ExitCode int
	TimedOut bool
}

// Runner executes a shell command. Implementations must stop the process
// tree when ctx is cancelled or timeout elapses, and must bound how much
// output they keep in memory.
type Runner interface {
	Run(ctx context.Context, command string, timeout time.Duration) (Result, error)
}
