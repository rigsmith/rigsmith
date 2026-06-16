package account

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if pidAlive(0x7FFFFFF0) {
		t.Error("implausibly high pid should not be alive")
	}
}
