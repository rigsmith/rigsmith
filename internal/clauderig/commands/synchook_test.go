package commands

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func TestSyncLock_SecondCallerIsTurnedAway(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	first, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("first acquire failed: got=%v err=%v", got, err)
	}
	if _, got, err := acquireSyncLock(staging); err != nil || got {
		t.Errorf("a second sync took the lock while one was held: got=%v err=%v", got, err)
	}
	first.Release()

	// Released, so the next caller gets it — otherwise one crashed sync would
	// stop syncing forever.
	second, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("lock not released: got=%v err=%v", got, err)
	}
	second.Release()
}

// A sync killed mid-run leaves its lock behind. Believing that forever is worse
// than two syncs overlapping, which git's own index.lock already handles.
func TestSyncLock_StaleLockIsBroken(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	old := time.Now().Add(-maxLockHold - time.Minute).Unix()
	if err := os.WriteFile(path, []byte("999999 "+strconv.FormatInt(old, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("a stale lock was believed: got=%v err=%v", got, err)
	}
	lock.Release()
}

// An unreadable lock cannot be interpreted, and refusing to sync forever over a
// file nobody can parse helps no one.
func TestSyncLock_UnparseableLockIsStale(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	if err := os.WriteFile(path, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, got, _ := acquireSyncLock(staging)
	if !got {
		t.Error("a malformed lock blocked syncing")
	}
	lock.Release()
}

// The lock must not live inside the repo, or it shows up as an uncommitted
// change and every status reads dirty.
func TestSyncLock_LivesOutsideTheRepo(t *testing.T) {
	base := t.TempDir()
	staging := filepath.Join(base, "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, _, _ := acquireSyncLock(staging)
	defer lock.Release()
	if _, err := os.Stat(filepath.Join(staging, ".sync.lock")); err == nil {
		t.Error("the lock was written inside the staging repo")
	}
	if _, err := os.Stat(filepath.Join(base, ".sync.lock")); err != nil {
		t.Errorf("lock not beside the repo: %v", err)
	}
}

func TestHookInterval_ConfigurableWithADisableEscape(t *testing.T) {
	for _, tc := range []struct {
		secs int
		want time.Duration
	}{
		{0, config.DefaultHookInterval}, // unset means the default
		{30, 30 * time.Second},
		{600, 10 * time.Minute},
		{-1, 0}, // opt out: sync on every turn, as it did before
	} {
		c := &config.Config{HookIntervalSeconds: tc.secs}
		if got := c.HookInterval(); got != tc.want {
			t.Errorf("HookInterval(%d) = %v, want %v", tc.secs, got, tc.want)
		}
	}
}
