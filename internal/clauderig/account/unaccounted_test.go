package account

import "testing"

func TestUnaccountedProcesses_QuietWhenNothingIsRunning(t *testing.T) {
	old := claudeProcessPIDs
	t.Cleanup(func() { claudeProcessPIDs = old })
	claudeProcessPIDs = func() ([]int, bool) { return nil, true }

	n, ok := UnaccountedProcesses(t.TempDir())
	if !ok || n != 0 {
		t.Errorf("got %d ok=%v, want a quiet 0", n, ok)
	}
}

// A failed scan is not "nothing is running", and must not read as a clean bill.
func TestUnaccountedProcesses_ReportsAFailedScan(t *testing.T) {
	old := claudeProcessPIDs
	t.Cleanup(func() { claudeProcessPIDs = old })
	claudeProcessPIDs = func() ([]int, bool) { return nil, false }

	if _, ok := UnaccountedProcesses(t.TempDir()); ok {
		t.Error("a failed process scan reported success")
	}
}
