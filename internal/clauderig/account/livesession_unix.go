//go:build !windows

package account

import (
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidAlive reports whether a process with the given pid currently exists.
// Signal 0 performs error-checking without delivering a signal: nil or EPERM
// (alive but owned by another user) means present; ESRCH means gone.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// terminate asks a process to exit gracefully (SIGTERM), letting editors like
// VS Code save and shut down cleanly.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// forceKill ends a process immediately (SIGKILL) — used only for stragglers that
// ignore SIGTERM within the grace period.
func forceKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

// claudeBinaryName is the executable Claude Code runs as. Matched on the
// BASENAME of the command path: macOS reports comm as the full
// `/opt/homebrew/bin/claude`, so an exact-string match (pgrep -x claude) finds
// nothing, while a substring match would also catch `clauderig` — this very
// tool — and make every switch refuse itself.
const claudeBinaryName = "claude"

// claudeProcessPIDs lists running Claude Code processes from the process table.
// ok=false means the scan itself failed — which is NOT the same answer as "none
// are running", and the switch guard must not read it as one.
func claudeProcessPIDsImpl() (pids []int, ok bool) {
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=,comm=").Output()
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, cerr := strconv.Atoi(fields[0])
		if cerr != nil {
			continue
		}
		if path.Base(filepath.ToSlash(strings.Join(fields[1:], " "))) == claudeBinaryName {
			pids = append(pids, pid)
		}
	}
	return pids, true
}

// processConfigDir reports a process's CLAUDE_CONFIG_DIR. known=false means the
// environment could not be read, which callers must treat as "assume live"
// rather than "no override".
func processConfigDirImpl(pid int) (dir string, known bool) {
	out, err := exec.Command("/bin/ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", false
	}
	for _, tok := range strings.Fields(string(out)) {
		if v, ok := strings.CutPrefix(tok, "CLAUDE_CONFIG_DIR="); ok {
			return v, true
		}
	}
	// The environment was readable and simply has no override: the process is
	// using ~/.claude.
	return "", true
}
