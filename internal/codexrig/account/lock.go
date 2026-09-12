package account

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
)

// swapLockName is the file that serialises codexrig's own credential swaps.
//
// It is NOT a lock Codex honours, and that difference matters enough to say out
// loud. clauderig can cooperate with Claude Code's refresh lock because Claude
// Code takes one; Codex 0.144.6 takes no lock around its own auth.json refresh —
// its only lock files are per-thread writers. So this guards codexrig against
// itself (two shells, a hook and a person) and nothing more. The protection
// against Codex refreshing mid-swap is a different mechanism: the switch refuses
// while Codex is running, writes atomically, and keeps a backup of what it
// displaced.
const swapLockName = ".account.lock"

// maxLockHold is how long a swap lock may be held before it is presumed
// abandoned. A swap is three file writes; anything near this is a crash.
const maxLockHold = 2 * time.Minute

// SwapLock is a held swap lock. Release is nil-safe so it can be deferred
// unconditionally.
type SwapLock struct {
	path  string
	token string
}

// AcquireSwap takes the swap lock, waiting up to wait for a holder to finish.
func (s *Store) AcquireSwap(wait time.Duration) (*SwapLock, error) {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Root, swapLockName)
	deadline := time.Now().Add(wait)
	for {
		lock, err := tryLock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if lockIsStale(path) {
			_ = os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("another codexrig account operation is in progress (%s)", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tryLock(path string) (*SwapLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	// A nonce as well as a pid: two acquisitions in one process within the same
	// clock tick would otherwise write the same token, and Release would then
	// happily delete somebody else's lock.
	var b [8]byte
	_, _ = rand.Read(b[:])
	token := fmt.Sprintf("%d %d %s", os.Getpid(), time.Now().UnixNano(), hex.EncodeToString(b[:]))
	if _, err := f.WriteString(token); err != nil {
		f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		// The file is already on disk carrying THIS process's pid, and the
		// caller gets no lock to Release — so lockIsStale sees a live pid and
		// refuses every swap until the two-minute hold expires.
		if rerr := os.Remove(path); rerr != nil {
			return nil, fmt.Errorf("%w; and the lock file could not be removed, so later swaps will wait out the stale timeout: %v", err, rerr)
		}
		return nil, err
	}
	return &SwapLock{path: path, token: token}, nil
}

// Release removes the lock, but only while it is still ours: a lock that was
// broken as stale and retaken belongs to somebody else now, and deleting it
// would hand a third caller the same lock.
func (l *SwapLock) Release() {
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
	// A holder whose process is gone is stale whatever its age. Waiting out the
	// full timeout for a hook that died with its shell blocks the machine for
	// no reason.
	if pid, err := strconv.Atoi(fields[0]); err == nil && !PIDAlive(pid) {
		return true
	}
	ns, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return true
	}
	return time.Since(time.Unix(0, ns)) > maxLockHold
}
