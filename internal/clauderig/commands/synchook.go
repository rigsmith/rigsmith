package commands

import (
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

// syncLock is a cross-platform advisory lock over the staging tree. It lives
// beside the repo rather than inside it, so it never shows up as an uncommitted
// change or has to be gitignored.
type syncLock struct{ path string }

// acquireSyncLock takes the lock, reporting whether it was free. O_EXCL rather
// than flock: the same code has to hold on Windows, and this needs no build tags
// and no dependency.
func acquireSyncLock(staging string) (*syncLock, bool, error) {
	path := filepath.Join(filepath.Dir(staging), ".sync.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
		f.Close()
		return &syncLock{path: path}, true, nil
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
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, false, nil
	}
	fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
	f.Close()
	return &syncLock{path: path}, true, nil
}

// Release drops the lock. Safe to call on a nil lock, so callers can defer it
// without first checking whether they got one.
func (l *syncLock) Release() {
	if l == nil {
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
	return time.Since(time.Unix(sec, 0)) > maxLockHold
}

// lastSuccessfulSync is when this machine last completed a sync, read from the
// journal rather than from a stamp file of its own — the journal already records
// exactly this, is already bounded, and cannot drift from what the activity feed
// shows.
//
// Failures do not count. Debouncing on a failed run would leave a machine that
// cannot push waiting out the interval before it is allowed to try again.
func lastSuccessfulSync(staging, machine string) (time.Time, bool) {
	recs, err := journal.Read(staging, 40)
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
