package commands

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/account"
)

// The sync lock serialises capture runs. Codex's Stop hook fires at the end of
// every turn in every open session, so without it several sessions walk, redact
// and stage the same tree at once.
//
// It is a file created with O_CREATE|O_EXCL rather than an flock, so the same
// code works on Windows with no build tags. It lives BESIDE the staging repo,
// never inside it, or it would show up as an uncommitted change in every status.

const syncLockName = ".sync.lock"

// maxLockHold is how long a lock may be held before it is presumed abandoned.
// Generous, because a first sync of a large home genuinely takes minutes.
const maxLockHold = 20 * time.Minute

// flushLockWait is how long a run that must not be skipped will wait for the
// holder: a person typing `codexrig sync`, or a session-end flush carrying the
// only chance to capture a rollout's tail.
const flushLockWait = 15 * time.Second

type syncLock struct {
	path  string
	token string
}

func acquireSyncLock(staging string, wait time.Duration) (*syncLock, bool, error) {
	dir := filepath.Dir(staging)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, syncLockName)
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			var b [8]byte
			_, _ = rand.Read(b[:])
			token := fmt.Sprintf("%d %d %s", os.Getpid(), time.Now().UnixNano(), hex.EncodeToString(b[:]))
			if _, werr := f.WriteString(token); werr != nil {
				f.Close()
				_ = os.Remove(path)
				return nil, false, werr
			}
			if cerr := f.Close(); cerr != nil {
				// Same reason as the write-error path above: the token is on
				// disk with this process's live pid and no lock comes back to
				// release it, so every later attempt waits out the full stale
				// timeout for a lock nobody holds.
				if rerr := os.Remove(path); rerr != nil {
					return nil, false, fmt.Errorf("%w; and the lock file could not be removed, so later syncs will wait out the stale timeout: %v", cerr, rerr)
				}
				return nil, false, cerr
			}
			return &syncLock{path: path, token: token}, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		if lockIsStale(path) {
			_ = os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, false, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Release removes the lock only while it is still ours. A lock that was broken
// as stale and retaken belongs to somebody else, and removing it would hand a
// third caller the same lock.
func (l *syncLock) Release() {
	if l == nil {
		return
	}
	b, err := os.ReadFile(l.path)
	if err != nil || strings.TrimSpace(string(b)) != l.token {
		return
	}
	_ = os.Remove(l.path)
}

func lockIsStale(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return true
	}
	// A holder whose process is gone is stale WHATEVER its age. Waiting out
	// twenty minutes for a hook that died with its terminal blocks every sync
	// on the machine for no reason at all.
	if pid, err := strconv.Atoi(fields[0]); err == nil && !account.PIDAlive(pid) {
		return true
	}
	ns, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return true
	}
	return time.Since(time.Unix(0, ns)) > maxLockHold
}
