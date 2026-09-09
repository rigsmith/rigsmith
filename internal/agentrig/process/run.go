// Package process owns command cancellation and cleanup for retained Git operations.
// Commands must be fresh, have finite stdin, and use trusted helpers that remain
// in their inherited Unix process group or Windows job. This is not a sandbox.
package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Run starts a command with platform ownership, terminates remaining helpers on
// every exit, and reaps the direct child before returning. Cancellation targets
// the group/job, including helpers holding inherited output pipes. The caller
// retains its staging lease until Run returns. Use exec.Command, not a command
// whose independent cancellation or Wait is managed elsewhere.
// Abrupt parent death on Unix still requires worker lifecycle supervision.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner, err := prepare(cmd)
	if err != nil {
		return err
	}
	return runOwned(ctx, cmd, owner)
}

func runOwned(ctx context.Context, cmd *exec.Cmd, owner *ownership) (err error) {
	defer func() {
		if closeErr := owner.close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close command ownership: %w", closeErr))
		}
	}()
	if err = cmd.Start(); err != nil {
		return err
	}
	if err = owner.started(cmd); err != nil {
		stopErr := owner.stop()
		killErr := cmd.Process.Kill()
		finishErr := owner.finish()
		waitErr := cmd.Wait()
		return errors.Join(err, stopErr, killErr, finishErr, waitErr)
	}
	done := make(chan error, 1)
	go func() {
		observed := owner.wait()
		stopped := owner.finish()
		if observed != nil {
			observed = fmt.Errorf("observe command exit: %w", observed)
		}
		if stopped != nil {
			stopped = fmt.Errorf("clean up command helpers: %w", stopped)
		}
		waitErr := cmd.Wait()
		if observed == nil && stopped == nil {
			done <- waitErr
		} else {
			done <- errors.Join(observed, stopped, waitErr)
		}
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		stopErr := owner.stop()
		err = errors.Join(stopErr, <-done)
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}
