package account

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// Instance is a running Codex process codexrig found.
type Instance struct {
	PID    int    `json:"pid"`
	Kind   string `json:"kind"`
	Source string `json:"source"` // "process" | "lock"
	Home   string `json:"home,omitempty"`
}

// binaryName is the Codex CLI's executable, matched on the BASENAME rather than
// as a substring. A substring match is how a tool finds itself: "codexrig"
// contains "codex", so a substring test would have every `codexrig` invocation
// report a running Codex and refuse its own switch.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "codex.exe"
	}
	return "codex"
}

// RunningInstances reports the Codex processes running under the given home.
// Dead pids are dropped and the result is sorted by pid, so two calls a moment
// apart do not reorder a list a person is reading.
func RunningInstances(home string) []Instance {
	out, _ := RunningInstancesScan(home)
	return out
}

// RunningInstancesScan is RunningInstances plus the distinction that decides
// whether a switch may proceed: ErrProcessScan means "I could not look", which
// is not the same answer as "nothing is running" and must never be treated as
// one.
func RunningInstancesScan(home string) ([]Instance, error) {
	seen := map[int]bool{}
	var out []Instance

	// Codex's own per-thread writer locks name a pid in neither their content
	// nor their name, so they establish only that SOMETHING has a thread open.
	// They are still worth reading: a lock whose thread is being written is a
	// reason to look harder, and on a machine where the process table is
	// unreadable they are the only evidence there is.
	for _, p := range lockFiles(home) {
		if pid, ok := pidFromLock(p); ok && pid > 1 && PIDAlive(pid) && !seen[pid] {
			seen[pid] = true
			out = append(out, Instance{PID: pid, Kind: "codex", Source: "lock"})
		}
	}

	pids, err := scanProcesses()
	for _, pid := range pids {
		if seen[pid] || !PIDAlive(pid) {
			continue
		}
		// A process running under a DIFFERENT home is an isolated account
		// session, and must not block a machine-wide switch — that is the
		// whole point of `codexrig account run`.
		if h, known := homeOf(pid); known && h != "" && !codexhome.SameDir(h, home) {
			continue
		}
		seen[pid] = true
		out = append(out, Instance{PID: pid, Kind: "codex", Source: "process"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	if err != nil {
		return out, ErrProcessScan
	}
	return out, nil
}

func lockFiles(home string) []string {
	dir := filepath.Join(home, "thread-writer-locks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".lock") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// pidFromLock reads a pid out of a lock file when it carries one. Codex's writer
// locks are zero-byte in 0.144.6, so this reports nothing today and costs one
// stat — it is here so a later Codex that starts writing a pid is used rather
// than ignored, and it never invents one.
func pidFromLock(p string) (int, bool) {
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return 0, false
	}
	var v struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(b, &v) == nil && v.PID > 0 {
		return v.PID, true
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
		return pid, true
	}
	return 0, false
}

// scanProcesses lists pids whose executable basename is the Codex CLI's.
func scanProcesses() ([]int, error) {
	if runtime.GOOS == "windows" {
		return scanProcessesWindows()
	}
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	want := binaryName()
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if path.Base(fields[len(fields)-1]) == want {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func scanProcessesWindows() ([]int, error) {
	out, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq "+binaryName(), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		cols := strings.Split(line, ",")
		if len(cols) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(strings.Trim(strings.TrimSpace(cols[1]), `"`)); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// homeOf reports the CODEX_HOME a process is running under, and whether that
// could be determined at all. An unknown answer is treated as "the machine's
// home" by the caller — assuming otherwise would let a switch run out from under
// a live session.
func homeOf(pid int) (string, bool) {
	if runtime.GOOS == "windows" {
		// Reading another process's environment on Windows needs a debug
		// privilege codexrig does not take, so every Codex process blocks a
		// switch there. --force is the documented way through, and paying
		// that cost is better than swapping a credential under a live
		// session.
		return "", false
	}
	out, err := exec.Command("/bin/ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", false
	}
	var procHome string
	for _, tok := range strings.Fields(string(out)) {
		if v, ok := strings.CutPrefix(tok, codexhome.EnvHome+"="); ok {
			return v, true
		}
		if v, ok := strings.CutPrefix(tok, "HOME="); ok {
			procHome = v
		}
	}
	// CODEX_HOME is genuinely absent, so that process is using the default home
	// — but the DEFAULT IS RELATIVE TO ITS OWN HOME, not to ours. Resolving it
	// from this process instead is how a switch decides that every Codex on the
	// machine, under any user, is running against the home it is about to
	// change. (Found exactly that way: four unrelated processes blocked a swap
	// in a sandbox whose HOME differed from theirs.)
	if procHome != "" {
		return filepath.Join(procHome, codexhome.DirName), true
	}
	// No HOME in the environment either. Refuse to guess: an unknown home is
	// treated as "it might be this one", which costs a --force and never costs
	// a live session its credential.
	return "", false
}

// UnaccountedProcesses counts live Codex processes under this home, and reports
// whether the scan could be done.
func UnaccountedProcesses(home string) (int, bool) {
	insts, err := RunningInstancesScan(home)
	if err != nil {
		return len(insts), false
	}
	return len(insts), true
}

// KillInstances ends the given processes: a polite signal, a grace period, then
// a hard one, then a short settle so the caller does not race the exit it asked
// for. It returns whatever survived.
func KillInstances(insts []Instance, grace time.Duration) (failed []Instance) {
	for _, in := range insts {
		_ = terminate(in.PID, false)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !anyAlive(insts) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, in := range insts {
		if PIDAlive(in.PID) {
			_ = terminate(in.PID, true)
		}
	}
	for i := 0; i < 20; i++ {
		if !anyAlive(insts) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, in := range insts {
		if PIDAlive(in.PID) {
			failed = append(failed, in)
		}
	}
	return failed
}

func anyAlive(insts []Instance) bool {
	for _, in := range insts {
		if PIDAlive(in.PID) {
			return true
		}
	}
	return false
}

// PIDAlive reports whether a process exists. pid 1 is real (a container
// entrypoint), and a process owned by another user answers true — "I am not
// allowed to signal it" is not "it is gone".
func PIDAlive(pid int) bool { return pid > 0 && pidAlive(pid) }
