package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

var errUnconfirmedCleanup = errors.New("cleanup inspection unavailable")

type uncertainOwnership struct{ *ownership }

func (o uncertainOwnership) finish() error {
	// Retire actual test processes, then model a failed cleanup observation.
	return errors.Join(o.ownership.finish(), errUnconfirmedCleanup)
}

func TestCommandFailureIsNotCleanupEvidence(t *testing.T) {
	for _, mode := range []string{"exit", "return"} {
		t.Run(mode, func(t *testing.T) {
			cmd := helperCommand(mode, t.TempDir()+"/child")
			owner, err := prepare(cmd)
			if err != nil {
				t.Fatal(err)
			}
			err, cleanupVerified := runOwnedChecked(t.Context(), cmd, uncertainOwnership{owner})
			if cleanupVerified || !errors.Is(err, errUnconfirmedCleanup) {
				t.Fatalf("uncertain cleanup accepted: cleanupVerified=%v err=%v", cleanupVerified, err)
			}
		})
	}
}

func TestRunRejectsAlreadyStartedCommandBeforeOwnership(t *testing.T) {
	dir := t.TempDir()
	ctx, release, err := storelock.Acquire(t.Context(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := helperCommand("leaf", filepath.Join(t.TempDir(), "ready"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	original := cmd.Process
	err = Run(WithSupervisor(ctx, os.Args[0]), cmd)
	if err == nil || err.Error() != "command already started" || cmd.Process != original || cmd.SysProcAttr != nil {
		t.Fatalf("pre-started command reached ownership setup: %v", err)
	}
	// Validation happens before intent; it neither adopts the external process
	// nor records cleanup for it. The caller still owns that process.
	if _, end, err := storelock.Acquire(ctx, dir, 0); err != nil {
		t.Fatal("rejection changed fence", err)
	} else {
		end()
	}
}
