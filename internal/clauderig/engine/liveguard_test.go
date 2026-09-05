package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// writeSessionRecord plants a ~/.claude/sessions/<pid>.json for a session whose
// process is this test binary — so the liveness check sees a real running pid
// without spawning anything.
func writeSessionRecord(t *testing.T, claudeHome, cwd, sessionID string) {
	t.Helper()
	pid := os.Getpid()
	body := fmt.Sprintf(`{"pid":%d,"entrypoint":"cli","cwd":%q,"sessionId":%q}`, pid, cwd, sessionID)
	write(t, claudeHome, filepath.Join("sessions", fmt.Sprintf("%d.json", pid)), body)
}

// The data-loss case this guard exists for: a session that has been running
// since before the last sync has a stale snapshot in staging. Restoring it
// unconditionally truncates the live transcript back to that snapshot, losing
// every turn since — silently, because restore reports success.
func TestRestore_KeepsLiveTranscript(t *testing.T) {
	const (
		cwd       = "/Users/jane/Git/demo"
		liveID    = "018ac192-6fa1-4400-98b3-6c58430d6ad7"
		staleID   = "77770000-0000-0000-0000-000000000000"
		staleText = "stale snapshot from the last sync\n"
		liveText  = "line 1\nline 2\nline 3 — written since the last sync\n"
	)
	slug := project.Flatten(cwd)

	staging := t.TempDir()
	write(t, staging, "cli/"+filepath.Join("projects", slug, liveID+".jsonl"), staleText)
	write(t, staging, "cli/"+filepath.Join("projects", slug, staleID+".jsonl"), "other session\n")
	write(t, staging, "cli/settings.json", `{"effortLevel":"high"}`)

	target := t.TempDir()
	// The live transcript on disk, longer than the staged snapshot.
	write(t, target, filepath.Join("projects", slug, liveID+".jsonl"), liveText)
	writeSessionRecord(t, target, cwd, liveID)

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: targetRootConfig(target), Machine: jane,
		TargetOverride: override("cli", target),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The live transcript is untouched.
	got := read(t, filepath.Join(target, "projects", slug, liveID+".jsonl"))
	if got != liveText {
		t.Fatalf("live transcript was overwritten:\n got %q\nwant %q", got, liveText)
	}

	// The guard is per-session, not per-project: the other session still lands.
	other := filepath.Join(target, "projects", slug, staleID+".jsonl")
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a non-live session in the same project was skipped too: %v", err)
	}
	// And unrelated config still restores.
	if _, err := os.Stat(filepath.Join(target, "settings.json")); err != nil {
		t.Errorf("settings.json was not restored: %v", err)
	}

	// The skip is reported, not silent.
	skips := rep.LiveSkips()
	if len(skips) != 1 {
		t.Fatalf("LiveSkips = %v, want exactly the live transcript", skips)
	}
	if !strings.Contains(skips[0], liveID) {
		t.Errorf("skip names the wrong file: %q", skips[0])
	}
}

// With nothing running, restore behaves exactly as before — the guard must not
// quietly start withholding files on an idle machine.
func TestRestore_NoLiveSessionsRestoresEverything(t *testing.T) {
	const cwd = "/Users/jane/Git/demo"
	slug := project.Flatten(cwd)
	sessionID := "018ac192-6fa1-4400-98b3-6c58430d6ad7"

	staging := t.TempDir()
	write(t, staging, "cli/"+filepath.Join("projects", slug, sessionID+".jsonl"), "staged\n")

	target := t.TempDir()
	write(t, target, filepath.Join("projects", slug, sessionID+".jsonl"), "local\n")

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: targetRootConfig(target), Machine: jane,
		TargetOverride: override("cli", target),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rep.LiveSkips()); got != 0 {
		t.Fatalf("skipped %d files with nothing running", got)
	}
	if got := read(t, filepath.Join(target, "projects", slug, sessionID+".jsonl")); got != "staged\n" {
		t.Fatalf("staged transcript did not restore: %q", got)
	}
}

// A record whose process is gone must not protect anything, or a crashed
// session would block its own transcript from ever being restored.
func TestLiveTranscripts_IgnoresDeadSessions(t *testing.T) {
	home := t.TempDir()
	// pid 0x7FFFFFFF is not a live process on any sane machine.
	write(t, home, filepath.Join("sessions", "2147483647.json"),
		`{"pid":2147483647,"entrypoint":"cli","cwd":"/Users/jane/Git/demo","sessionId":"dead"}`)

	if got := liveTranscripts(home); len(got) != 0 {
		t.Fatalf("a dead session still protected files: %v", got)
	}
}

// An IDE bridge lock has no transcript of its own, so it must protect nothing.
func TestLiveTranscripts_IgnoresIDELocks(t *testing.T) {
	home := t.TempDir()
	write(t, home, filepath.Join("ide", "12345.lock"),
		fmt.Sprintf(`{"pid":%d,"ideName":"VS Code"}`, os.Getpid()))

	if got := liveTranscripts(home); len(got) != 0 {
		t.Fatalf("an ide lock protected files: %v", got)
	}
}

// A machine that has never run Claude (no sessions dir) is the fresh-install
// path auto-restore takes; it must not error or protect anything.
func TestLiveTranscripts_MissingSessionsDir(t *testing.T) {
	if got := liveTranscripts(t.TempDir()); len(got) != 0 {
		t.Fatalf("empty home yielded %v", got)
	}
}

// The protected path is exactly projects/<flattened cwd>/<session id>.jsonl —
// the layout confirmed against a real ~/.claude.
func TestLiveTranscripts_PathShape(t *testing.T) {
	home := t.TempDir()
	writeSessionRecord(t, home, "/Users/jane/Git/demo", "abc-123")

	got := liveTranscripts(home)
	want := "projects/-Users-jane-Git-demo/abc-123.jsonl"
	if !got[want] {
		t.Fatalf("liveTranscripts = %v, want %q", got, want)
	}
}
