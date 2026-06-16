package account

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRunningInstances(t *testing.T) {
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
