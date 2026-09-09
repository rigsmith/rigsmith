//go:build linux || darwin

package process

import (
	"errors"
	"testing"
	"time"
)

func TestDeniedSignalRequiresObservedGroupExit(t *testing.T) {
	t.Run("exit-in-progress", func(t *testing.T) {
		inspections := 0
		stopped, err := confirmGroupExit(func() (bool, error) {
			inspections++
			return inspections < 3, nil
		}, time.Second)
		if err != nil || !stopped || inspections != 3 {
			t.Fatalf("did not wait for observed exit: %v %v %d", stopped, err, inspections)
		}
	})
	t.Run("still-alive", func(t *testing.T) {
		stopped, err := confirmGroupExit(func() (bool, error) { return true, nil }, 10*time.Millisecond)
		if err != nil || stopped {
			t.Fatalf("elapsed time was treated as exit: %v %v", stopped, err)
		}
	})
	t.Run("inspection-failed", func(t *testing.T) {
		inspections := 0
		stopped, err := confirmGroupExit(func() (bool, error) {
			inspections++
			if inspections == 1 {
				return true, nil
			}
			return false, errUnconfirmedCleanup
		}, time.Second)
		if stopped || !errors.Is(err, errUnconfirmedCleanup) {
			t.Fatalf("failed inspection was treated as exit: %v %v", stopped, err)
		}
	})
}
