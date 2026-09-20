package engine

import (
	"path/filepath"
	"testing"
)

// The same probe, and the same defect, as clauderig's: it reports how fast its
// own loop runs whenever the filesystem's clock is finer than that, and a tick
// finer than a kernel's narrows the trust window in stageClock to less than the
// span two writes can share. This engine had no test over it at all.
func TestProbeMtimeTickClaimsNothingFinerThanAKernelTick(t *testing.T) {
	for i := 0; i < 5; i++ {
		if got := probeMtimeTick(t.TempDir()); got < finestTick {
			t.Errorf("tick = %v, finer than any kernel tick (%v) — that is the loop's own pace, not the clock's", got, finestTick)
		}
	}
}

// Nowhere to write is no reason to assume the best, and the floor must not have
// swallowed the coarse assumption it sits under.
func TestAnUnusableDirectoryIsAssumedCoarse(t *testing.T) {
	if got := probeMtimeTick(filepath.Join(t.TempDir(), "no", "such", "dir")); got != coarseTick {
		t.Errorf("unusable directory gave %v, want %v", got, coarseTick)
	}
	if finestTick >= coarseTick {
		t.Fatalf("the floor %v has swallowed the coarse assumption %v", finestTick, coarseTick)
	}
}
