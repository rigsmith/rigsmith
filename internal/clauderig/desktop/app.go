package desktop

import (
	"errors"
	"fmt"
	"time"
)

// App controls Claude Desktop instances. Behind an interface so the command
// logic is testable without launching a real Electron app.
type App interface {
	// Launch starts a Claude Desktop instance bound to dataDir, detached from
	// the calling terminal.
	Launch(dataDir string) error
	// Running reports the PIDs of instances bound to exactly this dataDir.
	Running(dataDir string) ([]int, error)
	// Focus brings an already-running instance to the foreground. Best effort:
	// on platforms with no reliable way to raise one window of several, this may
	// raise whichever instance the OS considers frontmost.
	Focus(dataDir string) error
	// Quit ends the instance bound to dataDir: politely first, firmly after the
	// grace period.
	Quit(dataDir string, grace time.Duration) error
	// Installed reports whether Claude Desktop is present, and where.
	Installed() (path string, ok bool)
}

// ErrUnsupported means this OS has no Claude Desktop build we know how to drive.
var ErrUnsupported = errors.New("Claude Desktop profiles are not supported on this platform")

// ErrNotInstalled means the app itself is missing.
var ErrNotInstalled = errors.New("Claude Desktop is not installed")

// New returns the platform's App implementation.
func New() App { return newApp() }

// IsRunning answers "is this profile open", and reports an error rather than
// guessing when the process scan itself fails.
//
// Collapsing a failed scan into "closed" is not a cosmetic bug: `rm` deletes the
// profile directory, and doing that while Electron is still writing into it
// leaves the app writing to unlinked files. Every caller must be able to tell
// "closed" from "I could not look".
func IsRunning(a App, dataDir string) (bool, error) {
	pids, err := a.Running(dataDir)
	if err != nil {
		return false, err
	}
	return len(pids) > 0, nil
}

// userDataFlag is the Electron flag that binds an instance to a profile. It is
// also the needle every platform matches on to identify a running instance, so
// the flag and the match are defined in exactly one place.
func userDataFlag(dataDir string) string {
	return "--user-data-dir=" + dataDir
}

// waitGone polls until no instance is bound to dataDir, or the deadline passes.
// Reports whether they are all gone.
func waitGone(a App, dataDir string, deadline time.Time) bool {
	for {
		pids, err := a.Running(dataDir)
		if err == nil && len(pids) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// requireInstalled turns a missing app into one clear error rather than a
// confusing launch failure.
func requireInstalled(a App) error {
	if _, ok := a.Installed(); !ok {
		return fmt.Errorf("%w — install it from https://claude.ai/download", ErrNotInstalled)
	}
	return nil
}
