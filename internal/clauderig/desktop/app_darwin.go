//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultBundle is where Claude Desktop installs. A user-relocated bundle is
// found through the fallback in Installed().
const defaultBundle = "/Applications/Claude.app"

type darwinApp struct{}

func newApp() App { return darwinApp{} }

func (d darwinApp) Installed() (string, bool) {
	if fi, err := os.Stat(defaultBundle); err == nil && fi.IsDir() {
		return defaultBundle, true
	}
	// Relocated or per-user install: ask LaunchServices rather than guessing.
	out, err := exec.Command("/usr/bin/mdfind",
		"kMDItemCFBundleIdentifier == 'com.anthropic.claudefordesktop'").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasSuffix(line, ".app") {
			return line, true
		}
	}
	return "", false
}

// Launch starts a fresh instance through LaunchServices.
//
// `open -n` (rather than exec'ing the binary directly) is what detaches the app
// from this terminal: a direct launch stays in the caller's session, steals
// focus, and dies with the shell that started it. `--args` passes the profile
// through to Electron. Credit: guise found this the hard way.
func (d darwinApp) Launch(dataDir string) error {
	bundle, ok := d.Installed()
	if !ok {
		return requireInstalled(d)
	}
	cmd := exec.Command("/usr/bin/open", "-n", "-a", bundle, "--args", userDataFlag(dataDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launch Claude Desktop: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Running matches the full --user-data-dir= token so one profile's helper
// processes are never mistaken for another's (the profile paths share a prefix
// by construction: they are siblings under the same root).
//
// Two details the pattern needs, both load-bearing:
//
//   - `--` before it. The pattern itself starts with `--`, and without the
//     end-of-options separator pgrep rejects it as an illegal option — so this
//     returned an error for EVERY profile, which a swallowed error then turned
//     into "closed".
//   - regexp.QuoteMeta. pgrep -f takes an extended regular expression, but the
//     intent here is an exact literal match on a filesystem path, which may
//     contain `.`, `+`, `(` and friends.
func (d darwinApp) Running(dataDir string) ([]int, error) {
	pattern := regexp.QuoteMeta(userDataFlag(dataDir))
	out, err := exec.Command("/usr/bin/pgrep", "-f", "--", pattern).Output()
	if err != nil {
		// pgrep exits 1 for "no matches" — that is an answer, not a failure.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("scan for Claude Desktop processes: %w", err)
	}
	me := os.Getpid()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		pid, perr := strconv.Atoi(f)
		if perr != nil || pid == me {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// Focus raises Claude Desktop without opening another instance. macOS activates
// an application, not an individual window, so with several instances open this
// raises the app rather than provably this profile's window — which is why the
// caller says "already open" rather than promising a specific window.
func (d darwinApp) Focus(string) error {
	bundle, ok := d.Installed()
	if !ok {
		return requireInstalled(d)
	}
	_ = exec.Command("/usr/bin/open", "-a", bundle).Run()
	return nil
}

// Quit ends the instance and CONFIRMS it is gone before reporting success.
//
// `rm --force` quits and then deletes the profile directory, so a Quit that
// returns nil without checking would let the delete race a live Electron —
// which leaves the app writing into unlinked files. Every failure on the way
// (the follow-up scan, the signals) has to reach the caller.
func (d darwinApp) Quit(dataDir string, grace time.Duration) error {
	pids, err := d.Running(dataDir)
	if err != nil || len(pids) == 0 {
		return err
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	if waitGone(d, dataDir, time.Now().Add(grace)) {
		return nil
	}
	remaining, rerr := d.Running(dataDir)
	if rerr != nil {
		return fmt.Errorf("sent SIGTERM but could not confirm shutdown: %w", rerr)
	}
	for _, pid := range remaining {
		if kerr := syscall.Kill(pid, syscall.SIGKILL); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
			return fmt.Errorf("could not end Claude Desktop process %d: %w", pid, kerr)
		}
	}
	// Signals are asynchronous: confirm rather than assume.
	if !waitGone(d, dataDir, time.Now().Add(2*time.Second)) {
		return fmt.Errorf("Claude Desktop is still running for this profile after SIGKILL")
	}
	return nil
}

// Supported reports whether Anthropic ships Claude Desktop for this platform.
func Supported() bool { return true }

// OpenURL hands the deep link to LaunchServices, which delivers it to the
// registered handler for the scheme. No bundle is named: `open -a` would launch
// a fresh instance when none of the running ones takes it, and a second instance
// on a profile already open is the exact hazard `desktop open` guards against.
func (d darwinApp) OpenURL(rawurl string) error {
	cmd := exec.Command("/usr/bin/open", rawurl)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w: %s", rawurl, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunningDefault finds instances started with no --user-data-dir: the ordinary
// Claude Desktop. The pattern matches the MAIN binary only — a helper lives at
// Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/…, so it
// shares the bundle prefix but never ".app/Contents/MacOS/Claude".
func (d darwinApp) RunningDefault() ([]int, error) {
	bundle, ok := d.Installed()
	if !ok {
		return nil, nil // not installed: nothing can be running
	}
	main := filepath.Join(bundle, "Contents", "MacOS", "Claude")
	out, err := exec.Command("/usr/bin/pgrep", "-f", "--", regexp.QuoteMeta(main)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // no matches is an answer
		}
		return nil, fmt.Errorf("scan for Claude Desktop processes: %w", err)
	}
	me := os.Getpid()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		pid, perr := strconv.Atoi(f)
		if perr != nil || pid == me {
			continue
		}
		// A profile instance also matches the main binary; what separates it is
		// the flag. Only a process WITHOUT one is the default install.
		cmd, cerr := exec.Command("/bin/ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if cerr != nil {
			// ps exits 1 for "no such process" — the pid vanished between the
			// pgrep and this lookup, which is ordinary churn and genuinely
			// means it is not running. Anything else is a failed inspection,
			// and reporting that as "not the default app" would let the routing
			// guard send under unknown state.
			var ee *exec.ExitError
			if errors.As(cerr, &ee) && ee.ExitCode() == 1 {
				continue
			}
			return nil, fmt.Errorf("inspect Claude Desktop process %d: %w", pid, cerr)
		}
		if strings.Contains(string(cmd), userDataFlagName) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// Instances lists every Claude Desktop main process and the data directory it
// was launched against. Helpers are excluded by the pattern: they live under
// Contents/Frameworks/Claude Helper.app, which never contains the main binary's
// path. A process whose command line cannot be read is an error, not an
// omission — the caller uses this to decide whether sending is safe.
func (d darwinApp) Instances() ([]Instance, error) {
	bundle, ok := d.Installed()
	if !ok {
		return nil, nil
	}
	main := filepath.Join(bundle, "Contents", "MacOS", "Claude")
	out, err := exec.Command("/usr/bin/pgrep", "-f", "--", regexp.QuoteMeta(main)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("scan for Claude Desktop processes: %w", err)
	}
	me := os.Getpid()
	var found []Instance
	for _, f := range strings.Fields(string(out)) {
		pid, perr := strconv.Atoi(f)
		if perr != nil || pid == me {
			continue
		}
		cmd, cerr := exec.Command("/bin/ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if cerr != nil {
			var ee *exec.ExitError
			if errors.As(cerr, &ee) && ee.ExitCode() == 1 {
				continue // exited between the scan and the lookup: ordinary churn
			}
			return nil, fmt.Errorf("inspect Claude Desktop process %d: %w", pid, cerr)
		}
		found = append(found, Instance{PID: pid, DataDir: dataDirFromCommand(string(cmd)), Command: string(cmd)})
	}
	return found, nil
}
