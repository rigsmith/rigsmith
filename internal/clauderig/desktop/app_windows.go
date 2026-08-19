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
	sort.Strings(versioned) // lexical order is good enough to prefer a newer app-x.y.z
	exe := filepath.Join(base, versioned[len(versioned)-1], "claude.exe")
	if fi, err := os.Stat(exe); err == nil && !fi.IsDir() {
		return exe, true
	}
	return "", false
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
	const script = `Get-CimInstance Win32_Process -Filter "Name='claude.exe'" | ` +
		`Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress -AsArray`
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
	remaining, _ := w.Running(dataDir)
	for _, pid := range remaining {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	}
	return nil
}

// Supported reports whether Anthropic ships Claude Desktop for this platform.
func Supported() bool { return true }
