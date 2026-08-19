package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockCredentialsTakesBothLocksInClaudeCodeOrder(t *testing.T) {
	home := t.TempDir()
	release, _, err := LockCredentials(home, time.Second)
	if err != nil {
		t.Fatalf("LockCredentials: %v", err)
	}
	for _, dir := range []string{oauthRefreshLockDir(home), legacyCredentialLockDir(home)} {
		fi, serr := os.Stat(dir)
		if serr != nil {
			t.Fatalf("lock %s not held: %v", filepath.Base(dir), serr)
		}
		if !fi.IsDir() {
			t.Fatalf("lock %s is not a directory — proper-lockfile uses mkdir as the mutex", dir)
		}
	}
	release()
	for _, dir := range []string{oauthRefreshLockDir(home), legacyCredentialLockDir(home)} {
		if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
			t.Fatalf("lock %s survived release", filepath.Base(dir))
		}
	}
	release() // idempotent
}

// A lock a live Claude Code may still hold must never be stolen, however much
// the caller wants it: that is the whole point of the staleness rule.
func TestAcquireLockRefusesAFreshHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".oauth_refresh.lock")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := acquireLock(dir, credentialStaleness, 300*time.Millisecond)
	if !errors.Is(err, ErrClaudeBusy) {
		t.Fatalf("want ErrClaudeBusy, got %v", err)
	}
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Fatalf("gave up after %v — should have waited out the full timeout", waited)
	}
}

func TestAcquireLockTakesOverAStaleHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".oauth_refresh.lock")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * credentialStaleness)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	h, err := acquireLock(dir, credentialStaleness, time.Second)
	if err != nil {
		t.Fatalf("stale lock was not taken over: %v", err)
	}
	defer h.release()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) > credentialStaleness {
		t.Fatal("took over the lock but left the old mtime — the next waiter would steal it from us")
	}
}

// The toucher is what stops a legitimately-held lock from being stolen while a
// slow swap is still in flight.
//
// Observed by watching the mtime advance on its own — NOT by forging one.
// Backdating the directory is indistinguishable from another holder recreating
// it, which the ownership check now (correctly) treats as a takeover.
func TestHeldLockIsTouchedWhileHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".oauth_refresh.lock")
	h, err := acquireLock(dir, credentialStaleness, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer h.release()

	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2*touchInterval + 2*time.Second)
	for time.Now().Before(deadline) {
		fi, serr := os.Stat(dir)
		if serr == nil && fi.ModTime().After(before.ModTime()) {
			if !h.stillOurs() {
				t.Fatal("the lock stopped being recognised as ours after our own touch")
			}
			return // refreshed
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("lock mtime was never refreshed — a live holder would be judged stale and robbed")
}

// Holding half the pair while failing on the other is how two waiters deadlock,
// so a failed acquisition must leave nothing behind.
func TestLockCredentialsReleasesThePrimaryWhenTheLegacyLockIsContended(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(legacyCredentialLockDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LockCredentials(home, 200*time.Millisecond); !errors.Is(err, ErrClaudeBusy) {
		t.Fatalf("want ErrClaudeBusy, got %v", err)
	}
	if _, err := os.Stat(oauthRefreshLockDir(home)); !os.IsNotExist(err) {
		t.Fatal("primary lock left held after the legacy lock failed")
	}
}

func TestLegacyLockPathResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-claude")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link-claude")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Resolve the expectation too: on macOS the temp root itself lives under a
	// symlinked /var, so the raw path would never match.
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := legacyCredentialLockDir(link), realResolved+".lock"; got != want {
		t.Fatalf("legacy lock path = %q, want %q — Claude Code realpaths the config\n"+
			"home before appending .lock, so a symlinked ~/.claude must land on the same artifact", got, want)
	}
}

// A lock is a pathname, and a pathname can come to mean a different directory.
// If ours is judged stale and another holder recreates it, our toucher must not
// keep THEIR lock alive, and our release must not delete it.
func TestHeldLockDoesNotTouchOrRemoveALockItNoLongerOwns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".oauth_refresh.lock")
	h, err := acquireLock(dir, credentialStaleness, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a stale takeover: the directory is replaced by another holder's.
	// Note this is exactly the case inode identity CANNOT see — Linux reuses the
	// inode — so the check has to rest on the mtime we last set.
	if rerr := os.Remove(dir); rerr != nil {
		t.Fatal(rerr)
	}
	time.Sleep(10 * time.Millisecond) // ensure a distinguishable mtime
	if merr := os.Mkdir(dir, 0o755); merr != nil {
		t.Fatal(merr)
	}
	if h.stillOurs() {
		t.Fatal("a recreated lock directory is still reported as ours")
	}
	h.release()
	if _, serr := os.Stat(dir); os.IsNotExist(serr) {
		t.Fatal("release deleted a lock owned by another holder — stripping its exclusion")
	}
}

// The toucher must notice the takeover and report it, so the caller can stop
// before writing anything that is no longer exclusive.
func TestHeldLockReportsLostOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".oauth_refresh.lock")
	h, err := acquireLock(dir, credentialStaleness, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer h.release()
	if h.compromised() {
		t.Fatal("a freshly acquired lock reports itself compromised")
	}
	_ = os.Remove(dir)
	time.Sleep(10 * time.Millisecond)
	_ = os.Mkdir(dir, 0o755)
	deadline := time.Now().Add(2*touchInterval + 2*time.Second)
	for time.Now().Before(deadline) {
		if h.compromised() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the toucher never noticed that the lock had been taken over")
}
