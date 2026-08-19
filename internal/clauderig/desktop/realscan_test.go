package desktop

import (
	"os"
	"runtime"
	"testing"
)

// Gated real-machine check: the fake cannot catch a broken pgrep invocation,
// which is exactly how the `--`/QuoteMeta bug survived review.
func TestRealScanFindsTheRunningDesktop(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("CLAUDERIG_REAL_DESKTOP") == "" {
		t.Skip("set CLAUDERIG_REAL_DESKTOP=1 with Claude Desktop running")
	}
	dir := os.Getenv("HOME") + "/Library/Application Support/Claude"
	pids, err := New().Running(dir)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if len(pids) == 0 {
		t.Fatal("no processes matched the running Desktop's user-data-dir")
	}
	t.Logf("matched %d processes", len(pids))
}
