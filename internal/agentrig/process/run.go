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
// Unix workers can select an explicit parent-death supervisor with WithSupervisor.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if supervisor, ok := ctx.Value(supervisorKey{}).(supervisorCommand); ok {
		return runSupervised(ctx, cmd, supervisor)
	}
	return runDirect(ctx, cmd)
}

func runDirect(ctx context.Context, cmd *exec.Cmd) error {
	err, _ := runDirectChecked(ctx, cmd)
	return err
}

// Cleanup evidence is independent of the command status: cancellation, failed
// startup, and nonzero exits can all leave a clean store.
func runDirectChecked(ctx context.Context, cmd *exec.Cmd) (error, bool) {
	owner, err := prepare(cmd)
	if err != nil {
		return err, true
	}
	return runOwnedChecked(ctx, cmd, owner)
}

func runOwned(ctx context.Context, cmd *exec.Cmd, owner *ownership) error {
	err, _ := runOwnedChecked(ctx, cmd, owner)
	return err
}

type commandOwnership interface {
	started(*exec.Cmd) error
	wait() error
	stop() error
	finish() error
	close() error
}

func runOwnedChecked(ctx context.Context, cmd *exec.Cmd, owner commandOwnership) (err error, clean bool) {
	clean = true // No command has been created yet; close also retires any anchor.
	defer func() {
		if closeErr := owner.close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close command ownership: %w", closeErr))
			clean = false
		}
	}()
	if err = cmd.Start(); err != nil {
		return err, clean
	}
	clean = false
	if err = owner.started(cmd); err != nil {
		stopErr := owner.stop()
		killErr := cmd.Process.Kill()
		finishErr := owner.finish()
		waitErr := cmd.Wait()
		return errors.Join(err, stopErr, killErr, finishErr, waitErr), finishErr == nil
	}
	type outcome struct {
		err   error
		clean bool
	}
	done := make(chan outcome, 1)
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
			done <- outcome{waitErr, true}
		} else {
			done <- outcome{errors.Join(observed, stopped, waitErr), false}
		}
	}()
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		stopErr := owner.stop()
		result = <-done
		result.err = errors.Join(stopErr, result.err)
	}
	err, clean = result.err, result.clean
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err), clean
	}
	return err, clean
}
