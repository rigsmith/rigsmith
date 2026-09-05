package commands

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// maxLockHold is how long a lock file is believed. A sync killed mid-run leaves
// its lock behind, and a stale lock that is never broken would stop syncing
// altogether — which is a worse failure than two syncs overlapping.
const maxLockHold = 20 * time.Minute

// flushLockWait is how long a flush waits for a sync already in progress. A
// flush comes from a session that is ending: there is no later hook of its own
// to defer to, so it waits rather than skipping. Long enough to outlast a small
// sync, short enough not to hold up the shell someone is closing.
const flushLockWait = 15 * time.Second

// syncLock is a cross-platform advisory lock over the staging tree. It lives
// beside the repo rather than inside it, so it never shows up as an uncommitted
// change or has to be gitignored.
//
// token is what this holder wrote into the file. A lock held past maxLockHold
// is broken and retaken by someone else, and the original holder must not then
// delete the replacement on its way out.
type syncLock struct {
	path  string
	token string
}

// acquireSyncLock takes the lock, reporting whether it was free. O_EXCL rather
// than flock: the same code has to hold on Windows, and this needs no build tags
// and no dependency.
func acquireSyncLock(staging string) (*syncLock, bool, error) {
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	lock, err := writeLock(path)
	if err == nil {
		return lock, true, nil
	}
	if !os.IsExist(err) {
		return nil, false, err
	}
	if !lockIsStale(path) {
		return nil, false, nil
	}
	// Break it and take it. A race here means two syncs run, which git's own
	// index.lock will sort out — the same outcome as before this existed.
	_ = os.Remove(path)
	lock, err = writeLock(path)
	if err != nil {
		return nil, false, nil
	}
	return lock, true, nil
}

// acquireSyncLockWait takes the lock, waiting up to d for a run already in
// progress to finish. An ordinary hook passes 0: another sync is walking the
// same tree and will capture the same work, so there is nothing to wait for.
func acquireSyncLockWait(staging string, d time.Duration) (*syncLock, bool, error) {
	deadline := time.Now().Add(d)
	for {
		lock, got, err := acquireSyncLock(staging)
		if err != nil || got {
			return lock, got, err
		}
		if !time.Now().Before(deadline) {
			return nil, false, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// writeLock creates the lock file, failing if it is already there.
func writeLock(path string) (*syncLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d %d", os.Getpid(), time.Now().UnixNano())
	_, werr := fmt.Fprintln(f, token)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return nil, cmp.Or(werr, cerr)
	}
	return &syncLock{path: path, token: token}, nil
}

// Release drops the lock. Safe to call on a nil lock, so callers can defer it
// without first checking whether they got one.
func (l *syncLock) Release() {
	if l == nil {
		return
	}
	// Only if this is still our lock. A run that overran maxLockHold has had it
	// broken and retaken, and removing the file then would drop a lock someone
	// else is relying on.
	b, err := os.ReadFile(l.path)
	if err != nil || strings.TrimSpace(string(b)) != l.token {
		return
	}
	_ = os.Remove(l.path)
}

// lockIsStale reports whether a lock file is old enough to disbelieve. An
// unreadable or malformed lock counts as stale: it cannot be interpreted, and
// refusing to sync forever over a file nobody can parse helps no one.
func lockIsStale(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return true
	}
	sec, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return true
	}
	// Current lock tokens use UnixNano for uniqueness; older clients used
	// seconds. Reading nanos as seconds makes an abandoned lock immortal.
	at := time.Unix(sec, 0)
	if sec > 1e12 {
		at = time.Unix(0, sec)
	}
	return time.Since(at) > maxLockHold
}

// lastSuccessfulSync is when this machine last completed a sync, read from the
// journal rather than from a stamp file of its own — the journal already records
// exactly this, is already bounded, and cannot drift from what the activity feed
// shows.
//
// Failures do not count. Debouncing on a failed run would leave a machine that
// cannot push waiting out the interval before it is allowed to try again.
func lastSuccessfulSync(staging, machine string) (time.Time, bool) {
	// The whole feed, not the newest 40 of it. Read merges every machine's file
	// and applies the limit after merging, so on a machine syncing alongside
	// two others the newest 40 records can hold none of its own — and a
	// debounce that cannot find its last sync stops debouncing entirely, which
	// is the thrashing it exists to prevent. Already bounded: MaxRecords per
	// machine file.
	recs, err := journal.Read(staging, 0)
	if err != nil {
		return time.Time{}, false
	}
	for _, r := range recs { // newest first
		if r.Machine == machine && r.Op == journal.OpSync && r.OK() {
			return r.At, true
		}
	}
	return time.Time{}, false
}
