package process

import (
	"errors"
	"testing"
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
