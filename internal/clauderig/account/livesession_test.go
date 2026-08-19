package account

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// stubProcessTable replaces the machine's real process table for a test.
//
// RunningInstances consults BOTH the session registries and the process table,
// so without this a test asserting "these registry files produce these
// instances" also depends on whether a real Claude Code happens to be running on
// the developer's machine — and, since an unreadable environment is deliberately
// treated as live, that stray process gets counted. The guard's own behaviour is
// covered separately, with an explicit table, in the tests below.
func stubProcessTable(t *testing.T, pids []int, dirs map[int]string) {
	t.Helper()
	origPIDs, origDir := claudeProcessPIDs, processConfigDir
	claudeProcessPIDs = func() ([]int, bool) { return pids, true }
	processConfigDir = func(pid int) (string, bool) {
		d, ok := dirs[pid]
		return d, ok
	}
	t.Cleanup(func() { claudeProcessPIDs, processConfigDir = origPIDs, origDir })
}

func TestRunningInstances(t *testing.T) {
	stubProcessTable(t, nil, nil)
	home := t.TempDir()
	alive := os.Getpid()
	dead := 0x7FFFFFF0 // implausibly high pid — not running

	writeSession := func(name string, body string) {
		mustWrite(t, filepath.Join(home, "sessions", name), body)
	}
	// A live VS Code session, a dead CLI session, and a non-json file.
	writeSession("100.json", fmt.Sprintf(`{"pid":%d,"entrypoint":"claude-vscode"}`, alive))
	writeSession("200.json", fmt.Sprintf(`{"pid":%d,"entrypoint":"cli"}`, dead))
	writeSession("notes.txt", "ignore me")
	// A live IDE lock for a *different* pid, plus one duplicating the session pid.
	mustWrite(t, filepath.Join(home, "ide", "55.lock"), fmt.Sprintf(`{"pid":%d,"ideName":"VS Code"}`, alive))

	got := RunningInstances(home)
	if len(got) != 1 {
		t.Fatalf("RunningInstances = %d instances, want 1 (only the live pid, deduped): %+v", len(got), got)
	}
	if got[0].PID != alive || got[0].Kind != "claude-vscode" {
		t.Fatalf("instance = %+v, want pid=%d kind=claude-vscode (session wins over ide dup)", got[0], alive)
	}
}

func TestRunningInstances_EmptyAndMissing(t *testing.T) {
	stubProcessTable(t, nil, nil)
	// Missing ~/.claude entirely → no instances, no error.
	if got := RunningInstances(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Errorf("missing claude home → %v, want none", got)
	}
}

func TestKillInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses `sleep`")
	}
	c := exec.Command("sleep", "60")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	go func() { _ = c.Wait() }() // reap so the killed child doesn't linger as a zombie
	if !pidAlive(pid) {
		t.Fatal("spawned process should be alive")
	}
	if failed := KillInstances([]Instance{{PID: pid, Kind: "test"}}, 2*time.Second); len(failed) != 0 {
		t.Fatalf("KillInstances reported failures: %+v", failed)
	}
	if pidAlive(pid) {
		t.Error("process still alive after KillInstances")
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if pidAlive(0x7FFFFFF0) {
		t.Error("implausibly high pid should not be alive")
	}
}

// stubProcesses installs a fake process table for the duration of a test.
func stubProcesses(t *testing.T, pids []int, cfg map[int]string, unknown map[int]bool) {
	t.Helper()
	op, oc := claudeProcessPIDs, processConfigDir
	claudeProcessPIDs = func() ([]int, bool) { return pids, true }
	processConfigDir = func(pid int) (string, bool) {
		if unknown[pid] {
			return "", false
		}
		return cfg[pid], true
	}
	t.Cleanup(func() { claudeProcessPIDs, processConfigDir = op, oc })
}

// The regression that mattered: Claude Code 2.1.227 writes neither
// sessions/{pid}.json nor ide/{port}.lock, so with an empty ~/.claude the guard
// reported "nothing running" while a live session was open — and switching under
// one is what logs it out. The process table has to be consulted.
func TestRunningInstances_FoundWithNoRegistryFiles(t *testing.T) {
	home := t.TempDir() // no sessions/, no ide/ — a current install
	self := os.Getpid()
	stubProcesses(t, []int{self}, map[int]string{self: ""}, nil)

	got := RunningInstances(home)
	if len(got) != 1 || got[0].PID != self {
		t.Fatalf("got %+v, want the live process detected from the process table", got)
	}
	if got[0].Source != "process" {
		t.Errorf("Source = %q, want %q", got[0].Source, "process")
	}
}

// A session under `account run` uses its own profile and authenticates from it,
// so a machine-wide swap cannot disturb it. Blocking on those would make switch
// unusable on the very setup clauderig encourages.
func TestRunningInstances_IsolatedProfileSessionsDoNotBlock(t *testing.T) {
	home := t.TempDir()
	profile := t.TempDir()
	self := os.Getpid()
	stubProcesses(t, []int{self}, map[int]string{self: profile}, nil)

	if got := RunningInstances(home); len(got) != 0 {
		t.Errorf("an isolated profile session must not block a switch: %+v", got)
	}
	// ...but relative to its OWN home it is live, and must be reported.
	if got := RunningInstances(profile); len(got) != 1 {
		t.Errorf("relative to its own config dir it is live: %+v", got)
	}
}

// An unreadable environment must be assumed live. Over-reporting costs a
// refusal the user can override; under-reporting costs them their login.
func TestRunningInstances_UnknownEnvironmentAssumedLive(t *testing.T) {
	home := t.TempDir()
	self := os.Getpid()
	stubProcesses(t, []int{self}, nil, map[int]bool{self: true})

	if got := RunningInstances(home); len(got) != 1 {
		t.Errorf("a process whose config dir cannot be read must be treated as live: %+v", got)
	}
}

// Dead pids from the table are ignored, and a registry entry keeps its richer
// description rather than being replaced by the bare process one.
func TestRunningInstances_RegistryDescriptionWins(t *testing.T) {
	home := t.TempDir()
	self := os.Getpid()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := fmt.Sprintf(`{"pid":%d,"entrypoint":"claude-vscode","cwd":"/tmp/x"}`, self)
	if err := os.WriteFile(filepath.Join(home, "sessions", fmt.Sprintf("%d.json", self)), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	stubProcesses(t, []int{self, 999999}, map[int]string{self: ""}, nil)

	got := RunningInstances(home)
	if len(got) != 1 {
		t.Fatalf("dead pids must be dropped: %+v", got)
	}
	if got[0].Kind != "claude-vscode" || got[0].Cwd != "/tmp/x" {
		t.Errorf("registry detail should survive the process scan: %+v", got[0])
	}
}

// A failed scan must not read as "nothing is running". That is the fail-open the
// process check was added to eliminate, and it would let a switch overwrite the
// credential under a live session.
func TestRunningInstancesScanReportsAFailedScan(t *testing.T) {
	origPIDs, origDir := claudeProcessPIDs, processConfigDir
	claudeProcessPIDs = func() ([]int, bool) { return nil, false }
	processConfigDir = func(int) (string, bool) { return "", false }
	t.Cleanup(func() { claudeProcessPIDs, processConfigDir = origPIDs, origDir })

	got, err := RunningInstancesScan(t.TempDir())
	if !errors.Is(err, ErrProcessScan) {
		t.Fatalf("err = %v, want ErrProcessScan", err)
	}
	if len(got) != 0 {
		t.Fatalf("instances = %+v, want none found by other means", got)
	}
}

func TestRunningInstancesScanIsQuietWhenTheScanWorks(t *testing.T) {
	stubProcessTable(t, nil, nil)
	if got, err := RunningInstancesScan(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("RunningInstancesScan = %+v, %v; want none and no error", got, err)
	}
}
