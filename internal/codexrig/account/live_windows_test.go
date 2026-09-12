//go:build windows

package account

import (
	"math"
	"testing"
)

// uint32(pid) of a value past MaxUint32 wraps to a different pid, and for
// terminate that is an unrelated process.
func TestAPidThatDoesNotFitADWORDIsNeverTouched(t *testing.T) {
	if pidAlive(math.MaxUint32 + 1) {
		t.Error("a pid past the DWORD range was reported alive")
	}
	if err := terminate(math.MaxUint32+1, false); err == nil {
		t.Error("terminate accepted a pid past the DWORD range")
	}
}
