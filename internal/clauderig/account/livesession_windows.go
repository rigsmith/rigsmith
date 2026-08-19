//go:build windows

package account

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// pidAlive reports whether a process with the given pid currently exists by
// opening it with minimal rights. A successful open (handle closed immediately)
// means the process is present.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// Windows has no graceful per-process signal equivalent for GUI apps that's
// reliable from another process, so both paths use TerminateProcess.
func terminate(pid int) error { return killWindows(pid) }
func forceKill(pid int) error { return killWindows(pid) }

func killWindows(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// claudeProcessPIDs lists running Claude Code processes from the task list.
// ok=false means the scan itself failed — not "none are running".
func claudeProcessPIDsImpl() (pids []int, ok bool) {
	out, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq claude.exe", "/NH", "/FO", "CSV").Output()
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		// "claude.exe","1234","Console","1","50,000 K"
		cols := strings.Split(line, `","`)
		if len(cols) < 2 || !strings.Contains(strings.ToLower(cols[0]), "claude.exe") {
			continue
		}
		if pid, cerr := strconv.Atoi(strings.Trim(cols[1], `"`)); cerr == nil {
			pids = append(pids, pid)
		}
	}
	return pids, true
}

// processConfigDir cannot read another process's environment on Windows without
// significantly more machinery (opening each process and walking its PEB), so it
// reports unknown and every Claude Code process is treated as live. That errs
// toward refusing a switch, which costs an override; the opposite error costs a
// login.
//
// The cost is real and is documented rather than hidden: on Windows an isolated
// `clauderig account run` session is indistinguishable from a machine-wide one,
// so it blocks a switch it cannot actually be harmed by. `--force` is the
// intended escape, and it is safe precisely because those sessions authenticate
// from their own profile.
func processConfigDirImpl(int) (string, bool) { return "", false }
