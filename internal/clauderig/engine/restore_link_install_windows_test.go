package engine

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRestoreLinkWindowsFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		skip bool
	}{
		{windows.ERROR_ALREADY_EXISTS, true},
		{windows.STATUS_OBJECT_NAME_COLLISION, true},
		{windows.ERROR_NOT_SUPPORTED, true},
		{windows.STATUS_NOT_SUPPORTED, true},
		{windows.ERROR_PRIVILEGE_NOT_HELD, true},
		{windows.ERROR_ACCESS_DENIED, false},
		{windows.STATUS_ACCESS_DENIED, false},
		{windows.ERROR_DISK_FULL, false},
	} {
		if got := skipRestoreLinkError(fmt.Errorf("wrapped: %w", tc.err)); got != tc.skip {
			t.Errorf("skip %v = %v, want %v", tc.err, got, tc.skip)
		}
	}
}
