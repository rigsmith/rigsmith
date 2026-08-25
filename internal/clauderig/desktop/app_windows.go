//go:build windows

package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type windowsApp struct{}

func newApp() App { return windowsApp{} }

// Installed locates claude.exe. Claude Desktop is a Squirrel-style install under
// %LOCALAPPDATA%\AnthropicClaude: a stub launcher at the root plus versioned
// app-<version> directories beside it. Prefer the stub — it is stable across
// updates and forwards its arguments — and fall back to the newest versioned
// directory when the stub is absent.
func (w windowsApp) Installed() (string, bool) {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "AnthropicClaude")
	stub := filepath.Join(base, "claude.exe")
	if fi, err := os.Stat(stub); err == nil && !fi.IsDir() {
		return stub, true
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", false
	}
	var versioned []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "app-") {
			versioned = append(versioned, e.Name())
		}
	}
	if len(versioned) == 0 {
		return "", false
	}
	// Compare version components numerically: lexically, app-1.10.0 sorts BEFORE
	// app-1.9.0, so the newest install would be passed over for a stale one.
	sort.Slice(versioned, func(i, j int) bool {
		return compareAppVersions(versioned[i], versioned[j]) > 0 // newest first
	})
	// Walk newest-first rather than trusting the top entry: a partially removed
	// install can leave the directory without its executable, and an older
	// working install beside it is a better answer than none.
	for _, dir := range versioned {
		exe := filepath.Join(base, dir, "claude.exe")
		if fi, err := os.Stat(exe); err == nil && !fi.IsDir() {
			return exe, true
		}
	}
	return "", false
}

// compareAppVersions orders two `app-<version>` directory names by their numeric
// components: >0 when a is newer, <0 when b is, 0 when they compare equal. A
// non-numeric component sorts as 0, which keeps a malformed name from winning.
func compareAppVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "app-"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "app-"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		na, nb := versionPart(pa, i), versionPart(pb, i)
		if na != nb {
			if na > nb {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0
	}
	return n
}

// Launch starts a detached instance. CREATE_NEW_PROCESS_GROUP plus DETACHED_PROCESS
// is the Windows equivalent of macOS's `open -n`: the app must outlive this
// terminal and must not receive its Ctrl-C.
func (w windowsApp) Launch(dataDir string) error {
	exe, ok := w.Installed()
	if !ok {
		return requireInstalled(w)
	}
	cmd := exec.Command(exe, userDataFlag(dataDir))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windowsCreateNewProcessGroup | windowsDetachedProcess,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch Claude Desktop: %w", err)
	}
	// Release the child so this process doesn't hold it as a zombie-equivalent.
	return cmd.Process.Release()
}

const (
	windowsCreateNewProcessGroup = 0x00000200
	windowsDetachedProcess       = 0x00000008
)

// procRow is the shape asked of PowerShell — an array of {ProcessId, CommandLine}.
type procRow struct {
	ProcessID   int    `json:"ProcessId"`
	CommandLine string `json:"CommandLine"`
}

// Running matches on the full --user-data-dir= token in each process's command
// line. There is no pgrep on Windows, so CIM answers the same question; asking
// for JSON avoids parsing a localized table.
func (w windowsApp) Running(dataDir string) ([]int, error) {
	// -AsArray is PowerShell 6+, and `powershell` is Windows PowerShell 5.1 on a
	// standard install — with it, this failed outright on the target platform.
	// Wrapping the pipeline in @() and passing it via -InputObject keeps the
	// array shape for zero and one result on both.
	const script = `ConvertTo-Json -Compress -InputObject @(` +
		`Get-CimInstance Win32_Process -Filter "Name='claude.exe'" | ` +
		`Select-Object ProcessId,CommandLine)`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, fmt.Errorf("scan for Claude Desktop processes: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var rows []procRow
	if uerr := json.Unmarshal([]byte(trimmed), &rows); uerr != nil {
		return nil, fmt.Errorf("parse process list: %w", uerr)
	}
	needle := userDataFlag(dataDir)
	me := os.Getpid()
	var pids []int
	for _, r := range rows {
		// Command lines quote paths containing spaces, and the default profile
		// root does. Compare with quotes stripped so both spellings match.
		if r.ProcessID != me && strings.Contains(stripQuotes(r.CommandLine), needle) {
			pids = append(pids, r.ProcessID)
		}
	}
	return pids, nil
}

func stripQuotes(s string) string { return strings.ReplaceAll(s, `"`, "") }

// Focus cannot raise one instance of several from the command line without
// window-handle work that is not worth the dependency here. Reporting the app as
// already open, rather than raising the wrong window, is the honest behaviour.
func (w windowsApp) Focus(string) error { return nil }

// Quit ends the instance and CONFIRMS it is gone before reporting success —
// `rm --force` deletes the profile directory immediately afterwards, and doing
// that under a live Electron leaves it writing into unlinked files.
func (w windowsApp) Quit(dataDir string, grace time.Duration) error {
	pids, err := w.Running(dataDir)
	if err != nil || len(pids) == 0 {
		return err
	}
	for _, pid := range pids {
		// No /F: ask for a clean shutdown so Electron flushes its own state.
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run()
	}
	if waitGone(w, dataDir, time.Now().Add(grace)) {
		return nil
	}
	remaining, rerr := w.Running(dataDir)
	if rerr != nil {
		return fmt.Errorf("asked Claude Desktop to close but could not confirm shutdown: %w", rerr)
	}
	for _, pid := range remaining {
		if out, kerr := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput(); kerr != nil {
			// The process may simply have exited between the scan and the kill;
			// the final check below decides whether that is what happened.
			_ = out
		}
	}
	if !waitGone(w, dataDir, time.Now().Add(2*time.Second)) {
		return fmt.Errorf("Claude Desktop is still running for this profile after a forced taskkill")
	}
	return nil
}

// Supported reports whether Anthropic ships Claude Desktop for this platform.
func Supported() bool { return true }

// OpenURL hands the deep link to the shell's protocol handler.
//
// `cmd /c start` is used rather than ShellExecute because the URL must not be
// parsed as a command: `start` takes an empty title argument first, so a link
// beginning with a quote can never be read as the window title. The URL is
// passed as its own argv entry, so cmd's own metacharacters never see it.
func (w windowsApp) OpenURL(rawurl string) error {
	cmd := exec.Command("cmd", "/c", "start", "", rawurl)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w: %s", rawurl, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunningDefault finds instances started with no --user-data-dir: the ordinary
// Claude Desktop. Same process listing as Running, inverted — a row WITHOUT the
// flag is the default install rather than a profile.
func (w windowsApp) RunningDefault() ([]int, error) {
	const script = `ConvertTo-Json -Compress -InputObject @(` +
		`Get-CimInstance Win32_Process -Filter "Name='claude.exe'" | ` +
		`Select-Object ProcessId,CommandLine)`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, fmt.Errorf("scan for Claude Desktop processes: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var rows []procRow
	if uerr := json.Unmarshal([]byte(trimmed), &rows); uerr != nil {
		return nil, fmt.Errorf("parse process list: %w", uerr)
	}
	me := os.Getpid()
	var pids []int
	for _, r := range rows {
		if r.ProcessID != me && !strings.Contains(stripQuotes(r.CommandLine), userDataFlagName) {
			pids = append(pids, r.ProcessID)
		}
	}
	return pids, nil
}
