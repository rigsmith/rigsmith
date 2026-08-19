package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// stageSidecar writes a staged Desktop sidecar naming a transcript.
func stageSidecar(t *testing.T, staging, tree, id, cliSessionID string) string {
	t.Helper()
	rel := filepath.Join("desktop", tree, "acct", "org", "local_"+id+".json")
	body := `{"sessionId":"local_` + id + `","title":"t"}`
	if cliSessionID != "" {
		body = `{"sessionId":"local_` + id + `","cliSessionId":"` + cliSessionID + `","title":"t"}`
	}
	write(t, staging, rel, body)
	return filepath.Join(staging, rel)
}

func stageTranscript(t *testing.T, staging, slug, sessionID string) {
	t.Helper()
	write(t, staging, filepath.Join("cli", "projects", slug, sessionID+".jsonl"), "{}\n")
}

func TestPruneOrphanedSidecars(t *testing.T) {
	staging := t.TempDir()
	stageTranscript(t, staging, "-Users-john-p", "live-1")
	stageTranscript(t, staging, "-Users-john-q", "live-2")

	kept := stageSidecar(t, staging, "claude-code-sessions", "a", "live-1")
	orphan := stageSidecar(t, staging, "claude-code-sessions", "c", "gone-9")
	noRef := stageSidecar(t, staging, "claude-code-sessions", "d", "")

	n, err := pruneOrphanedSidecars(staging)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}
	for _, p := range []string{kept, noRef} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("should have been kept: %s", filepath.Base(p))
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphaned sidecar should have been removed")
	}
}

// A Cowork session's transcript lives inside its sandbox, which is not synced at
// all — so its cliSessionId can never resolve here. Judging those sidecars by this
// rule would delete every one of them on every sync, for a reason unrelated to
// retention, so the tree is exempt.
func TestPruneOrphanedSidecars_LeavesAgentModeAlone(t *testing.T) {
	staging := t.TempDir()
	stageTranscript(t, staging, "-Users-john-p", "live-1")
	agent := stageSidecar(t, staging, "local-agent-mode-sessions", "b", "sandbox-only-session")

	n, err := pruneOrphanedSidecars(staging)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pruned = %d, want 0", n)
	}
	if _, err := os.Stat(agent); err != nil {
		t.Error("Cowork sidecar must not be pruned by the CLI-transcript rule")
	}
}

// The guard that matters most: with no transcript index to compare against, this
// must remove NOTHING. Reading "no transcripts" as "everything is orphaned" would
// wipe every sidecar on a Desktop-only sync.
func TestPruneOrphanedSidecars_FailsOpenWithoutTranscripts(t *testing.T) {
	for _, tc := range []struct{ name, slug string }{
		{"no projects dir at all", ""},
		{"projects dir exists but is empty", "-Users-john-p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staging := t.TempDir()
			if tc.slug != "" {
				if err := os.MkdirAll(filepath.Join(staging, "cli", "projects", tc.slug), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			s := stageSidecar(t, staging, "claude-code-sessions", "a", "some-session")

			n, err := pruneOrphanedSidecars(staging)
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("pruned = %d, want 0", n)
			}
			if _, err := os.Stat(s); err != nil {
				t.Error("sidecar must survive when there is no index to judge it by")
			}
		})
	}
}

// A sidecar's own mtime is not an activity signal — Desktop rewrites it on its own
// schedule, and a months-old sidecar routinely names a transcript written days
// ago. Retention must follow the transcript, so an ancient sidecar whose
// transcript is current survives.
func TestPruneOrphanedSidecars_OldSidecarWithLiveTranscriptSurvives(t *testing.T) {
	staging := t.TempDir()
	stageTranscript(t, staging, "-Users-john-p", "live-1")
	s := stageSidecar(t, staging, "claude-code-sessions", "a", "live-1")
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(s, old, old); err != nil {
		t.Fatal(err)
	}

	if n, err := pruneOrphanedSidecars(staging); err != nil || n != 0 {
		t.Fatalf("pruned = %d, err = %v; a 90-day-old sidecar with a live transcript must survive", n, err)
	}
	if _, err := os.Stat(s); err != nil {
		t.Error("sidecar should still exist")
	}
}

// End to end: when retention ages a transcript out of staging, the sidecar that
// named it goes in the same sync — the two trees prune as one unit.
func TestSync_RetentionPrunesTranscriptAndItsSidecar(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	staging := t.TempDir()

	// Previously staged: one fresh session, one whose transcript is long expired.
	stageTranscript(t, staging, "-Users-john-p", "fresh-1")
	stageTranscript(t, staging, "-Users-john-p", "stale-1")
	freshSide := stageSidecar(t, staging, "claude-code-sessions", "fresh", "fresh-1")
	staleSide := stageSidecar(t, staging, "claude-code-sessions", "stale", "stale-1")

	old := time.Now().Add(-90 * 24 * time.Hour)
	stalePath := filepath.Join(staging, "cli", "projects", "-Users-john-p", "stale-1.jsonl")
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}

	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{
		StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john,
		RetentionDays:  30,
		SourceOverride: override("cli", liveCli, "desktop", liveDesk),
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("aged transcript should have been pruned")
	}
	if _, err := os.Stat(staleSide); !os.IsNotExist(err) {
		t.Error("sidecar for the aged transcript should have been pruned with it")
	}
	if _, err := os.Stat(freshSide); err != nil {
		t.Error("sidecar for the surviving transcript should have been kept")
	}
	if rep.SidecarsPruned != 1 {
		t.Errorf("SidecarsPruned = %d, want 1", rep.SidecarsPruned)
	}
}
