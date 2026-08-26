package desktop

import (
	"errors"
	"fmt"
	"strings"
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
	// RunningDefault reports the PIDs of instances running on the app's OWN data
	// directory — the ordinary Claude Desktop, started with no profile flag.
	//
	// It cannot be expressed as Running(<some dir>): a default launch carries no
	// --user-data-dir at all, so it matches no dataDir and is invisible to that
	// scan. It still competes for a deep link like any other window, which is
	// exactly why it needs its own question.
	RunningDefault() ([]int, error)
	// Instances reports EVERY running Claude Desktop main process with the data
	// directory it was launched against ("" for the profile-less install).
	//
	// This exists because the routing guard kept being wrong in the same way.
	// Asking "which of the profiles I know about are running" means enumerating
	// instances, and five rounds of review found five kinds this missed: a
	// profile whose metadata will not parse, one launched with a --user-data-dir
	// outside clauderig's store, one whose store entry is a directory symlink,
	// the profile-less install, and a scan that failed. Every one of them still
	// competes for a scheme-routed deep link.
	//
	// Counting PROCESSES has no such list to be incomplete. A window either
	// exists or it does not.
	Instances() ([]Instance, error)
	// Focus brings an already-running instance to the foreground. Best effort:
	// on platforms with no reliable way to raise one window of several, this may
	// raise whichever instance the OS considers frontmost.
	Focus(dataDir string) error
	// Quit ends the instance bound to dataDir: politely first, firmly after the
	// grace period.
	Quit(dataDir string, grace time.Duration) error
	// Installed reports whether Claude Desktop is present, and where.
	Installed() (path string, ok bool)
	// OpenURL hands a claude:// deep link to Claude Desktop.
	//
	// It cannot be aimed at a particular profile. The OS routes a URL by SCHEME,
	// to whichever registered instance it picks — there is no per-instance
	// address, and the profile flag that separates instances is a launch
	// argument, not something a URL can carry. Callers that care which profile
	// receives it must make that instance the only, or at least the frontmost,
	// one first — and say so when they cannot be sure.
	OpenURL(rawurl string) error
}

// Instance is one running Claude Desktop main process. DataDir is the value of
// its --user-data-dir flag, or "" when it was launched without one.
type Instance struct {
	PID     int
	DataDir string
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
	return userDataFlagName + dataDir
}

// userDataFlagName is the flag without a value — what tells a profile instance
// apart from the default install, which carries no such flag at all.
const userDataFlagName = "--user-data-dir="

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

// WaitRunning blocks until an instance bound to dataDir appears, or the
// deadline passes. Reports whether one did.
//
// A deep link needs a live instance to receive it: with none running, the OS
// resolves the scheme by LAUNCHING the app — and that launch carries no profile
// flag, so it starts the machine-wide install instead of the profile that was
// asked for. Waiting is what stops "open this session in my work profile" from
// quietly opening a personal window.
func WaitRunning(a App, dataDir string, deadline time.Time) (bool, error) {
	for {
		// Deadline FIRST. Accepting a running pid before checking it meant a
		// profile that appeared after the timeout still counted as ready, so
		// the bound was advisory rather than a bound.
		if time.Now().After(deadline) {
			return false, nil
		}
		pids, err := a.Running(dataDir)
		if err != nil {
			// A scan that FAILS is not an app that did not start. Swallowing it
			// here would burn the whole deadline and then blame the app, while
			// the caller is about to decide where a deep link goes on the
			// strength of this answer.
			return false, err
		}
		if len(pids) > 0 {
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// dataDirFromCommand pulls the --user-data-dir value out of a command line,
// returning "" when the flag is absent (the profile-less install).
//
// The value runs to the end of the argument. A path containing spaces is not
// recoverable from a flattened command line, so it is taken up to the next
// " --" instead — enough to compare instances, which is all the caller needs.
func dataDirFromCommand(cmd string) string {
	i := strings.Index(cmd, userDataFlagName)
	if i < 0 {
		return ""
	}
	rest := cmd[i+len(userDataFlagName):]
	if j := strings.Index(rest, " --"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(rest), `"`))
}
