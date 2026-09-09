// Package storelock coordinates operations on one local staging store. Locks
// live beside the store, outside Git, and are owned by the OS until released or
// the process exits. Every writer must participate; this is not a distributed
// lock and does not coordinate separate clones, raw Git, or older clients.
package storelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("another operation is using the staging store; retry after it finishes")

type contextKey struct{}

type lease struct {
	mu   sync.Mutex
	file *os.File
	info os.FileInfo
	refs int
}

// SameActiveLease reports whether both contexts carry the same live ownership
// capability. Derived contexts and nested acquisitions share it; an expired or
// unrelated capability cannot authorize rebinding command supervision.
func SameActiveLease(a, b context.Context) bool {
	left, _ := a.Value(contextKey{}).(*lease)
	right, _ := b.Value(contextKey{}).(*lease)
	if left == nil || left != right || a.Err() != nil || b.Err() != nil {
		return false
	}
	left.mu.Lock()
	defer left.mu.Unlock()
	return left.refs > 0
}

// Acquire returns an operation context and an idempotent release function.
// Zero wait tries once; a positive wait bounds contention, independently of the
// operation's lifetime. Cancellation returns the caller's context error.
//
// Pass the returned context only to sequential nested operations on this store.
// It is an ownership capability, not permission to start concurrent writers.
// Nested acquisitions borrow the lease; the last release closes it. An expired
// context lease cannot bypass acquisition. Nesting different stores is refused
// to avoid lock-order deadlocks. Independent operations use independent contexts.
func Acquire(ctx context.Context, dir string, wait time.Duration) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if wait < 0 {
		return ctx, nil, fmt.Errorf("store lock wait must not be negative")
	}
	path, err := lockPath(dir)
	if err != nil {
		return ctx, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ctx, nil, fmt.Errorf("open store lock: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return ctx, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return ctx, nil, fmt.Errorf("store lock is not a regular file: %s", path)
	}
	if held, _ := ctx.Value(contextKey{}).(*lease); held != nil {
		held.mu.Lock()
		if held.refs > 0 {
			if !os.SameFile(info, held.info) {
				held.mu.Unlock()
				f.Close()
				return ctx, nil, fmt.Errorf("cannot nest operations on different staging stores")
			}
			if err := checkFence(held.file); err != nil {
				held.mu.Unlock()
				f.Close()
				return ctx, nil, err
			}
			held.refs++
			held.mu.Unlock()
			f.Close()
			return ctx, held.release(), nil
		}
		held.mu.Unlock()
	}
	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return ctx, nil, err
		}
		got, err := tryLock(f)
		if err != nil {
			f.Close()
			return ctx, nil, fmt.Errorf("lock staging store: %w", err)
		}
		if got {
			if err := ctx.Err(); err != nil {
				f.Close()
				return ctx, nil, err
			}
			if err := checkFence(f); err != nil {
				f.Close()
				return ctx, nil, err
			}
			l := &lease{file: f, info: info, refs: 1}
			return context.WithValue(ctx, contextKey{}, l), l.release(), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			f.Close()
			return ctx, nil, ErrBusy
		}
		timer := time.NewTimer(min(remaining, 25*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return ctx, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *lease) release() func() {
	return sync.OnceFunc(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.refs--
		if l.refs == 0 {
			// Never unlink: a waiter may already have this inode open. Replacing
			// it would let a new arrival lock a different inode concurrently.
			_ = l.file.Close()
		}
	})
}

func lockPath(dir string) (string, error)         { return resolveLockPath(dir, true) }
func existingLockPath(dir string) (string, error) { return resolveLockPath(dir, false) }

func resolveLockPath(dir string, createParent bool) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("staging store path is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil {
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(abs)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("staging store is not a directory: %s", abs)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", fmt.Errorf("filesystem root cannot be a staging store")
	}
	// Do not create the store itself: git clone requires a missing or empty
	// destination. Resolving its parent also handles aliases before first clone.
	if createParent {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	// Use a sibling name rather than a hash of path spelling: on case-folding
	// filesystems, case aliases then address the same lock file naturally.
	return filepath.Join(parent, "."+filepath.Base(abs)+".agentrig.lock"), nil
}
