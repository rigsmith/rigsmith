package commandrun

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestRunnerOwnsCancellationAndPreservesFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := errors.Join(errors.New("command failed"), errors.New("cleanup unverified"))
	ctx = WithRunner(ctx, func(got context.Context, cmd *exec.Cmd) error {
		if cmd.Cancel != nil || cmd.Process != nil {
			t.Fatal("runner did not receive a fresh command with exclusive cancellation ownership")
		}
		cancel()
		if got.Err() != context.Canceled {
			t.Fatal("runner lost operation cancellation")
		}
		return failure
	})
	cmd := Command(ctx, os.Args[0])
	if err := Run(ctx, cmd); err != failure {
		t.Fatalf("cleanup failure identity lost: %v", err)
	}
}

func TestInvalidSelectionNeverFallsBack(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		ctx = WithRunner(ctx, nil)
		if canceled {
			ctx = WithRunner(ctx, func(context.Context, *exec.Cmd) error {
				t.Fatal("canceled operation reached runner")
				return nil
			})
			cancel()
		}
		cmd := Command(ctx, os.Args[0])
		err := Run(ctx, cmd)
		cancel()
		if err == nil || cmd.Process != nil || !Configured(ctx) {
			t.Fatalf("invalid selection fell back: process=%v err=%v", cmd.Process, err)
		}
	}
}
