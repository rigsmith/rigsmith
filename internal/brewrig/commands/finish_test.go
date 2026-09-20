package commands

import (
	"errors"
	"testing"

	"github.com/rigsmith/rigsmith/internal/brewrig/engine"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

// finishMutation is the shared contract behind `apply` and `sync --apply`.
// Three separate bugs have lived in this four-line shape: sync not
// republishing at all, apply returning before republishing on a partial
// failure, and the republish error being dropped in favour of the action
// error. These pin all three.

func changed() *engine.Result {
	return &engine.Result{Installed: []inventory.Ref{{Kind: inventory.Formula, Name: "jq"}}}
}

func TestNothingChangedMeansNoRepublish(t *testing.T) {
	called := false
	err := finishMutation(&engine.Result{}, nil, func() error { called = true; return nil })

	if called {
		t.Error("republished after a run that changed nothing; the published inventory was still accurate")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// The bug in `sync --apply`: Homebrew changed and nothing was republished.
func TestAnythingChangedMeansRepublish(t *testing.T) {
	called := false
	if err := finishMutation(changed(), nil, func() error { called = true; return nil }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !called {
		t.Error("Homebrew changed but the shared inventory was not republished")
	}
}

// The bug in `apply`: it returned on the first action error, so a partial run
// left the shared copy stale even though some packages really had installed.
func TestAPartialFailureStillRepublishes(t *testing.T) {
	actionErr := errors.New("1 of 2 actions failed")
	called := false

	err := finishMutation(changed(), actionErr, func() error { called = true; return nil })

	if !called {
		t.Error("a partially failed run did not republish; what DID install is real")
	}
	if !errors.Is(err, actionErr) {
		t.Errorf("err = %v, want the action error preserved", err)
	}
}

// And the third: the republish failure must not be swallowed by the action
// error. It is the more alarming of the two — it says Homebrew moved while the
// shared copy did not.
func TestBothErrorsSurvive(t *testing.T) {
	actionErr := errors.New("1 of 2 actions failed")
	pubErr := errors.New("push rejected")

	err := finishMutation(changed(), actionErr, func() error { return pubErr })

	if !errors.Is(err, actionErr) {
		t.Errorf("err = %v, want the action error preserved", err)
	}
	if !errors.Is(err, pubErr) {
		t.Errorf("err = %v, want the republish error preserved", err)
	}
}

func TestARepublishFailureAloneIsReported(t *testing.T) {
	pubErr := errors.New("push rejected")

	err := finishMutation(changed(), nil, func() error { return pubErr })

	if !errors.Is(err, pubErr) {
		t.Errorf("err = %v, want the republish error", err)
	}
}
