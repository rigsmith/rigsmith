package account

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cooperate with Claude Code's own advisory locks while swapping its credential.
//
// The race this closes is narrow and real: Claude Code's token refresh reads the
// credential, does a network round trip, and saves the result — and a swap that
// lands inside that window is overwritten by the refreshed OLD account's token.
// The machine ends up authenticating as the account it just switched away from,
// or, when the refresh saves a partial document, as nobody at all. Held under
// the lock, Claude Code's own double-checked re-read sees the swapped
// (non-expired) credential and abandons the refresh instead.
//
// The protocol is Claude Code's, read out of the shipped bundle rather than
// assumed — verified against 2.1.227, the build on the machine this was written
// on (its `FIa`/`ePa` helpers):
//
//   - The lock artifact is a DIRECTORY. `mkdir` atomicity is the mutex. This is
//     npm `proper-lockfile`, which Claude Code uses for both locks.
//   - The refresh path takes TWO locks, in this order: the primary
//     `<config-home>/.oauth_refresh.lock`, then the legacy
//     `<realpath(config-home)>.lock` (`~/.claude.lock`), kept for compatibility
//     with external tools. Both run `stale: 60000, update: 5000`.
//   - A holder touches the directory's mtime every 5s; a lock is stale — and may
//     be taken over — only once its mtime is more than 60s old. Never steal a
//     younger one: a live holder's toucher can stall well past 10s (a suspended
//     laptop, a blocked event loop) while still legitimately owning the lock.
//   - On a contended legacy lock Claude Code releases the primary and retries,
//     up to 5 times with 1–2s jittered sleeps. Mirroring the pair AND the order
//     is what keeps a waiting clauderig and a waiting Claude Code from
//     deadlocking against each other.
//
// Credit: the discovery that these locks exist and must be cooperated with —
// and the reverse-engineering of the two-lock protocol — is from claude-swap by
// realiti4 (MIT), PR #167. This is an independent Go implementation of the same
// protocol, re-verified against the bundle.

const (
	// credentialStaleness is Claude Code's own 60s: a lock younger than this
	// belongs to a live holder and must not be taken over.
	credentialStaleness = 60 * time.Second
	// touchInterval keeps our own lock alive. Slightly faster than Claude
	// Code's 5s, for margin.
	touchInterval = 3 * time.Second
	// defaultLockTimeout bounds the wait. Claude Code holds the credential lock
	// for one token-endpoint round trip — sub-second to a few seconds — so 9s
	// outlasts it comfortably without hanging the CLI. This is PER LOCK: the
	// pair's worst case is roughly twice this.
	defaultLockTimeout = 9 * time.Second
)

// ErrClaudeBusy means Claude Code held a credential lock for longer than we were
// willing to wait — almost always a token refresh in flight.
var ErrClaudeBusy = errors.New("Claude Code is holding its credential lock")

// heldLock is one acquired lock directory and the goroutine keeping it fresh.
type heldLock struct {
	dir  string
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// release stops the toucher and removes the lock directory. Safe to call twice.
func (h *heldLock) release() {
	h.once.Do(func() {
		close(h.stop)
		<-h.done
		_ = os.Remove(h.dir)
	})
}

// acquireLock takes a proper-lockfile-compatible directory lock, waiting up to
// timeout and taking over a holder whose mtime is older than stale.
func acquireLock(dir string, stale, timeout time.Duration) (*heldLock, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("take lock %s: %w", filepath.Base(dir), err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (%s). Retry in a few seconds",
				ErrClaudeBusy, filepath.Base(dir))
		}
		fi, serr := os.Stat(dir)
		if serr != nil {
			continue // released between Mkdir and Stat — retry immediately
		}
		if time.Since(fi.ModTime()) > stale {
			// Dead holder by the protocol's own rule: remove and retake. Losing
			// the remove/mkdir race to another waiter just loops again.
			if rerr := os.Remove(dir); rerr != nil {
				time.Sleep(50 * time.Millisecond) // can't remove it either; don't spin hot
			}
			continue
		}
		time.Sleep(250*time.Millisecond + time.Duration(rand.Int63n(int64(250*time.Millisecond))))
	}

	h := &heldLock{dir: dir, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		t := time.NewTicker(touchInterval)
		defer t.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				now := time.Now()
				if err := os.Chtimes(h.dir, now, now); err != nil {
					return // stolen or removed; nothing left to keep alive
				}
			}
		}
	}()
	return h, nil
}

// oauthRefreshLockDir is Claude Code's primary refresh lock.
func oauthRefreshLockDir(claudeHome string) string {
	return filepath.Join(claudeHome, ".oauth_refresh.lock")
}

// legacyCredentialLockDir is the compatibility lock external tools are expected
// to take (`~/.claude.lock`). Claude Code resolves the config home through
// realpath before appending `.lock`, so a symlinked ~/.claude lands on the same
// artifact either way — mirror that or we would lock a different path than the
// process we are trying to exclude.
func legacyCredentialLockDir(claudeHome string) string {
	resolved := claudeHome
	if r, err := filepath.EvalSymlinks(claudeHome); err == nil {
		resolved = r
	}
	return resolved + ".lock"
}

// LockCredentials holds both of Claude Code's credential locks, in Claude Code's
// own order, for the duration of a credential mutation. The returned release is
// idempotent and safe to defer.
//
// A caller that cannot take the locks must NOT proceed: writing the credential
// mid-refresh is precisely the failure this prevents.
func LockCredentials(claudeHome string, timeout time.Duration) (release func(), err error) {
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	primary, err := acquireLock(oauthRefreshLockDir(claudeHome), credentialStaleness, timeout)
	if err != nil {
		return nil, err
	}
	legacy, err := acquireLock(legacyCredentialLockDir(claudeHome), credentialStaleness, timeout)
	if err != nil {
		// Claude Code releases the primary on legacy contention rather than
		// holding it while it waits. Do the same: holding one half while
		// failing is how two waiters deadlock.
		primary.release()
		return nil, err
	}
	return func() {
		legacy.release()
		primary.release()
	}, nil
}
