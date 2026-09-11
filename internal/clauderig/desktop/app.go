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
	// LaunchDefault starts the MACHINE-WIDE install: a new instance carrying no
	// --user-data-dir at all.
	//
	// It cannot be expressed as Launch(""), and not only because that would name
	// a profile directory. With any instance already running, asking the OS to
	// open the app activates THAT one — the profile window you are trying to get
	// away from — so this has to insist on a new instance and then say nothing
	// about a profile, which is what leaves it on the app's own data directory.
	LaunchDefault() error
	// Raise brings ONE running instance to the front, named by pid.
	//
	// Focus cannot do this: it activates the application, and every instance
	// shares one application as far as the OS is concerned, so with several
	// windows open it raises whichever the OS prefers. Naming the process is the
	// only way to say which one is meant.
	//
	// Returns ErrRaiseUnsupported where that cannot be done at all, so a caller
	// can say "it is running, switch to it yourself" rather than reporting a
	// failure over a window that is fine.
	Raise(pid int) error
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
	// Command is the flattened command line, kept for diagnostics. Nothing
	// decides identity from it: a flattened command cannot be split back into
	// arguments, so DataDir below is a best-effort read used only for display.
	Command string
}

// ErrUnsupported means this OS has no Claude Desktop build we know how to drive.
var ErrUnsupported = errors.New("Claude Desktop profiles are not supported on this platform")

// ErrNotInstalled means the app itself is missing.
var ErrNotInstalled = errors.New("Claude Desktop is not installed")

// HasDataDir reports whether a command line carries a --user-data-dir at all,
// which is what tells a profile instance from the machine-wide install.
//
// Asked of the COMMAND, never of Instance.DataDir. That field is parsed out of
// a flattened command line and is documented as best-effort: a path containing
// " --" cannot be recovered from one, and the failure is silent — an empty
// DataDir, which reads as "no profile flag" and therefore as the machine-wide
// app. A profile shown as the main app is the one mistake this whole package
// exists to prevent.
func HasDataDir(command string) bool {
	return flagAt(stripCommandQuotes(command), 0) >= 0
}

// flagAt finds --user-data-dir= starting at an ARGUMENT boundary, at or after
// from, and returns the index just past the flag. -1 when there is none.
//
// The boundary matters at both ends, and each end was a separate review finding.
// Without it, `--diagnostic=--user-data-dir=/store/work/data` contains the flag
// without carrying it, and a window would be matched to a profile by a string
// that happens to appear inside one of its other arguments.
func flagAt(command string, from int) int {
	for at := from; at < len(command); {
		i := strings.Index(command[at:], userDataFlagName)
		if i < 0 {
			return -1
		}
		k := at + i
		if k == 0 || command[k-1] == ' ' || command[k-1] == '\t' {
			return k + len(userDataFlagName)
		}
		at = k + 1
	}
	return -1
}

// CommandHasDataDir reports whether a command line names exactly this data
// directory. The comment above dataDirFromCommand has promised this function
// for a while; it is here now, and the callers that decide identity use it.
//
// EXACTLY, which a substring test does not give: "/store/work/data" is a prefix
// of "/store/work/data-old", so a plain Contains would let `desktop open work`
// raise a window belonging to another profile — and the popover would show one
// pid on two rows. The value has to end where the argument ends.
func CommandHasDataDir(command, dataDir string) bool {
	cmd := stripCommandQuotes(command)
	for at := 0; ; {
		start := flagAt(cmd, at)
		if start < 0 {
			return false
		}
		value := cmd[start:]
		// The value has to BE this directory, not merely begin with it:
		// /store/work/data is a prefix of /store/work/data-old. So it ends
		// where the argument ends — at the next space, or at the end.
		if strings.HasPrefix(value, dataDir) {
			after := value[len(dataDir):]
			if after == "" || after[0] == ' ' || after[0] == '\t' {
				return true
			}
		}
		at = start
	}
}

// stripCommandQuotes normalises the quoting Windows puts around paths with
// spaces, so one needle matches on both platforms.
func stripCommandQuotes(command string) string {
	return strings.ReplaceAll(command, `"`, "")
}

// MainPIDs returns the MAIN processes of the instance bound to dataDir.
//
// Running() cannot be used for this. It matches the --user-data-dir token
// anywhere in a command line, and every Electron helper inherits that flag: one
// profile answers with its main process and a dozen renderers and utilities. A
// helper is not an application, has no windows, and raising one is either an
// error or a no-op depending on how you ask — so anything that means "this
// profile's window" has to start from the process list that excludes them.
func MainPIDs(a App, dataDir string) ([]int, error) {
	instances, err := a.Instances()
	if err != nil {
		return nil, err
	}
	want := CanonicalDir(dataDir)
	var pids []int
	for _, inst := range instances {
		// The command line first, because it is the thing that cannot be
		// truncated: an exact token match on --user-data-dir=<dir> is what
		// Running has always used. CanonicalDir second, so a store entry that
		// is a directory symlink still matches the window running behind it.
		if CommandHasDataDir(inst.Command, dataDir) ||
			(inst.DataDir != "" && CanonicalDir(inst.DataDir) == want) {
			pids = append(pids, inst.PID)
		}
	}
	return pids, nil
}

// RaiseSupported reports whether this platform can bring one named window
// forward at all.
//
// Asked BEFORE offering the action, not discovered by attempting it: a menu
// that raises nothing on every click, or answers every click with the same
// dialog, is worse than a menu that says plainly it cannot.
func RaiseSupported() bool { return raiseSupported }

// ErrRaiseUnsupported means this platform has no way to bring one named
// instance forward. Not a failure: the window is there, and the caller should
// say so rather than imply something went wrong.
var ErrRaiseUnsupported = errors.New("bringing one Claude Desktop window forward is not supported on this platform")

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
// Best effort, and used for DISPLAY only. A path containing spaces cannot be
// recovered from a flattened command line, so the value is taken up to the next
// " --" — which is wrong for a directory that itself contains that sequence.
// Identity comparisons go through CommandHasDataDir instead, which does not
// depend on this succeeding.
func dataDirFromCommand(cmd string) string {
	i := -1
	// At an argument boundary only, so the flag is not found inside the
	// executable path that precedes it.
	for at := 0; at < len(cmd); {
		j := strings.Index(cmd[at:], userDataFlagName)
		if j < 0 {
			break
		}
		k := at + j
		if k == 0 || cmd[k-1] == ' ' || cmd[k-1] == '"' {
			i = k
			break
		}
		at = k + 1
	}
	if i < 0 {
		return ""
	}
	rest := cmd[i+len(userDataFlagName):]
	if j := strings.Index(rest, " --"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(rest), `"`))
}
