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
	for _, mode := range []string{"nil", "canceled-nil", "canceled-callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = WithRunner(ctx, nil)
			if mode == "canceled-callback" {
				ctx = WithRunner(ctx, func(context.Context, *exec.Cmd) error {
					t.Fatal("canceled operation reached runner")
					return nil
				})
			}
			if mode != "nil" {
				cancel()
			}
			cmd := Command(ctx, os.Args[0])
			err := Run(ctx, cmd)
			if err == nil || cmd.Process != nil || !Configured(ctx) {
				t.Fatalf("invalid selection fell back: process=%v err=%v", cmd.Process, err)
			}
			if mode != "nil" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestRunnerFailureSurvivesIgnoredErrorAndDerivedContext(t *testing.T) {
	failure := errors.New("unverified cleanup")
	calls := 0
	ctx := WithRunner(t.Context(), func(context.Context, *exec.Cmd) error { calls++; return failure })
	_ = Run(ctx, Command(ctx, os.Args[0])) // Model a legacy boolean/fallback caller.
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := Check(derived); err != failure {
		t.Fatalf("failure disappeared: %v", err)
	}
	cmd := Command(derived, os.Args[0])
	if err := Run(derived, cmd); err != failure || calls != 1 || cmd.Process != nil {
		t.Fatalf("later command ran: calls=%d process=%v err=%v", calls, cmd.Process, err)
	}
	if err := Check(t.Context()); err != nil {
		t.Fatalf("failure leaked into another operation: %v", err)
	}
}
