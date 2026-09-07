// Package process owns command cancellation and cleanup for queued Git transport.
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
	defer owner.close()
	if err = cmd.Start(); err != nil {
		return err
	}
	if err = owner.started(cmd); err != nil {
		_ = owner.stop()
		_ = cmd.Process.Kill()
		_ = owner.finish()
		_ = cmd.Wait()
		return err
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
