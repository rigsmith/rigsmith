package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
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
	mins := func(n int) *int { return &n }
	for _, tc := range []struct {
		name string
		set  *int
		want time.Duration
	}{
		// Unset and 0 must differ, which is the whole reason this is a pointer.
		{"unset takes the default", nil, config.DefaultHookInterval},
		{"zero turns it off", mins(0), 0},
		{"one minute", mins(1), time.Minute},
		{"ten minutes", mins(10), 10 * time.Minute},
		{"negative reads as off", mins(-1), 0},
	} {
		c := &config.Config{HookIntervalMinutes: tc.set}
		if got := c.HookInterval(); got != tc.want {
			t.Errorf("%s: HookInterval() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A holder whose lock was broken for running too long must not delete the lock
// that replaced it — the run holding that one is still going.
func TestSyncLock_ReleaseOnlyDropsItsOwn(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	first, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("first acquire: %v %v", got, err)
	}
	// Age it past maxLockHold so the next caller breaks it, as a killed sync
	// would leave it.
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	old := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().Add(-2*maxLockHold).Unix())
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	second, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("stale lock was not broken: %v %v", got, err)
	}

	first.Release() // the original holder, finishing late
	if _, err := os.Stat(path); err != nil {
		t.Fatal("releasing a broken lock deleted the replacement")
	}
	second.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the holder could not release its own lock")
	}
}

// A second sync gets nothing while the first holds the lock, and the wait form
// gives up rather than proceeding alongside it.
func TestSyncLock_WaitGivesUpRatherThanOverlap(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	held, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("acquire: %v %v", got, err)
	}
	defer held.Release()

	if _, got, err := acquireSyncLockWait(staging, 300*time.Millisecond); err != nil || got {
		t.Errorf("a second sync took the lock: got=%v err=%v", got, err)
	}
}

// Read merges every machine's file and caps afterwards, so on a busy shared repo
// the newest records can be entirely other machines'. A debounce that cannot
// find its own last sync stops debouncing, which is the thrashing it is for.
func TestLastSuccessfulSync_FoundBehindOtherMachinesRecords(t *testing.T) {
	staging := t.TempDir()
	mine := time.Now().Add(-2 * time.Minute)
	if err := journal.Append(staging, journal.Record{
		At: mine, Machine: "mine", Op: journal.OpSync, Outcome: journal.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
	// Two other machines burying it.
	for i := range 60 {
		for _, m := range []string{"other-a", "other-b"} {
			if err := journal.Append(staging, journal.Record{
				At:      time.Now().Add(-time.Duration(60-i) * time.Second),
				Machine: m, Op: journal.OpSync, Outcome: journal.OutcomeOK,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	at, ok := lastSuccessfulSync(staging, "mine")
	if !ok {
		t.Fatal("this machine's own last sync was not found, so it would never debounce")
	}
	if at.Sub(mine).Abs() > time.Second {
		t.Errorf("found %s, want %s", at, mine)
	}
}

// The lock a sync actually writes has to be recognised as stale once it is old
// enough. Built with lockToken — the function writeLock uses — because the
// original bug was precisely that the writer and the parser disagreed about the
// unit, and every test here hand-wrote its fixture with seconds while
// production wrote nanoseconds. A fixture that cannot drift is the fix.
func TestLockIsStale_ReadsTheTokenWriteLockWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync.lock")
	for _, tc := range []struct {
		age  time.Duration
		want bool
	}{
		{0, false},
		{maxLockHold / 2, false},
		{2 * maxLockHold, true},
		{72 * time.Hour, true},
	} {
		token := lockToken(os.Getpid(), time.Now().Add(-tc.age))
		if err := os.WriteFile(path, []byte(token+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := lockIsStale(path); got != tc.want {
			t.Errorf("a lock held for %v: stale=%v, want %v (token %q)", tc.age, got, tc.want, token)
		}
	}
}

// Locks written by v1.13.0's predecessors carry seconds. One of those can be on
// disk during the upgrade, and it still has to be breakable.
func TestLockIsStale_StillReadsTheOlderSecondsToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync.lock")
	old := fmt.Sprintf("%d %d", os.Getpid(), time.Now().Add(-2*maxLockHold).Unix())
	if err := os.WriteFile(path, []byte(old+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !lockIsStale(path) {
		t.Error("a seconds-format lock from an older clauderig was not breakable")
	}
}

// The whole point, end to end: a sync that was killed must not lock the machine
// out of syncing for good.
func TestAcquireSyncLock_BreaksTheLockAKilledSyncLeftBehind(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if _, got, err := acquireSyncLock(staging); err != nil || !got {
		t.Fatalf("first acquire: %v %v", got, err)
	}
	// The killed sync never released it. Age it past maxLockHold using the same
	// token the holder wrote.
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	if err := os.WriteFile(path, []byte(lockToken(99999, time.Now().Add(-2*maxLockHold))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatal("the abandoned lock was never broken — this machine would stop syncing for good")
	}
	lock.Release()
}

func TestSyncLockCreatesConfigDirectory(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "new-config", "repo")
	lock, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("first sync cannot lock a new config directory: %v", err)
	}
	lock.Release()
}
