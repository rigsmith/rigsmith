// Package commandrun lets an operation supply command ownership without making
// core Git helpers depend on a vendor's worker, locks, or process supervisor.
// Selection controls subprocess execution only: it does not acquire a lease or
// make a calling workflow safe against normalized errors or in-process writes.
package commandrun

import (
	"context"
	"errors"
	"os/exec"
	"sync"
)

type runnerKey struct{}
type selectedRunner struct {
	run     func(context.Context, *exec.Cmd) error
	mu      sync.Mutex
	failure error
}

// WithRunner selects a trusted synchronous runner for this operation. The runner
// must own startup, cancellation, output copying, child cleanup and waiting, and
// return only after cleanup is finished or its uncertainty has been reported.
// Callers retain any required staging lease until Run returns. A nil runner is
// invalid and fails closed at Run; it never restores default execution.
func WithRunner(ctx context.Context, run func(context.Context, *exec.Cmd) error) context.Context {
	return context.WithValue(ctx, runnerKey{}, &selectedRunner{run: run})
}

// Configured reports an explicit override, including an invalid nil override.
// Streaming/interactive callers that cannot honor it must reject the operation.
func Configured(ctx context.Context) bool {
	_, ok := ctx.Value(runnerKey{}).(*selectedRunner)
	return ok
}

// Command constructs a fresh command. The default retains exec.CommandContext's
// behavior; selected runners receive a plain command and exclusively own its
// cancellation. Callers may configure streams, environment and working directory.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if Configured(ctx) {
		return exec.Command(name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

// Run uses the selected runner or the ordinary exec.Cmd runner. Errors pass
// through unchanged so callers can distinguish a direct command exit from a
// joined/wrapped cleanup error. Do not call Start/Wait independently.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if runner, ok := ctx.Value(runnerKey{}).(*selectedRunner); ok {
		if err := Check(ctx); err != nil {
			return err
		}
		err := runner.run(ctx, cmd)
		if err == nil {
			err = ctx.Err()
		}
		if _, directExit := err.(*exec.ExitError); err != nil && !directExit {
			runner.mu.Lock()
			if runner.failure == nil {
				runner.failure = err
			}
			runner.mu.Unlock()
		}
		return err
	}
	return cmd.Run()
}

// Check reports an operation's first runner failure, invalid selection, or
// cancellation. Only a direct ExitError is an ordinary command answer; all
// other failures stop further commands, even if a legacy caller ignores them.
// Call this before in-process mutations and before reporting workflow success.
// Contexts derived from the selection share the failure; starting a new runner
// selection must never be used to recover an uncertain operation. Callers must
// use the context sequentially and retain its lease. Without selection Check is
// a no-op, preserving ordinary synchronous workflow behavior.
func Check(ctx context.Context) error {
	runner, ok := ctx.Value(runnerKey{}).(*selectedRunner)
	if !ok {
		return nil
	}
	runner.mu.Lock()
	failure := runner.failure
	runner.mu.Unlock()
	if failure != nil {
		return failure
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runner.run == nil {
		return errors.New("command runner is nil")
	}
	return nil
}
