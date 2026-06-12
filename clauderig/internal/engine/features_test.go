package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/core/pathmap"
)

func fexists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSync_RetentionDropsOldTranscripts(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-p/old.jsonl", "old session")
	write(t, live, "projects/-p/new.jsonl", "new session")
	old := time.Now().AddDate(0, 0, -40)
	if err := os.Chtimes(filepath.Join(live, "projects", "-p", "old.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if fexists(filepath.Join(staging, "cli", "projects", "-p", "old.jsonl")) {
		t.Error("old transcript should have been dropped by retention")
	}
	if !fexists(filepath.Join(staging, "cli", "projects", "-p", "new.jsonl")) {
		t.Error("recent transcript should be synced")
	}
	if rep.Roots[0].RetentionByAge != 1 {
		t.Errorf("RetentionByAge = %d, want 1", rep.Roots[0].RetentionByAge)
	}
}

func TestSync_IncrementalSkipsUnchanged(t *testing.T) {
	live := t.TempDir()
	write(t, live, "skills/a/SKILL.md", "body")
	write(t, live, "projects/-p/s.jsonl", "transcript")
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	r1, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Roots[0].Unchanged != 0 {
		t.Errorf("first sync Unchanged = %d, want 0", r1.Roots[0].Unchanged)
	}

	r2, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m})
	if err != nil {
		t.Fatal(err)
	}
	// the two raw files (skill + transcript) are unchanged on the second run
	if r2.Roots[0].Unchanged != 2 {
		t.Errorf("second sync Unchanged = %d, want 2", r2.Roots[0].Unchanged)
	}
	if r2.Roots[0].Files != 0 {
		t.Errorf("second sync rewrote %d files, want 0", r2.Roots[0].Files)
	}
}

func TestRestore_PruneRemovesStaleConfigNotProjects(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/skills/keep/SKILL.md", "keep")
	write(t, staging, "cli/projects/-p/s.jsonl", "x")

	target := t.TempDir()
	write(t, target, "skills/stale/SKILL.md", "stale — deleted upstream")
	write(t, target, "projects/-local/mine.jsonl", "my local session") // additive

	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: targetRootConfig(target), Machine: m, Prune: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fexists(filepath.Join(target, "skills", "stale", "SKILL.md")) {
		t.Error("stale skill should have been pruned")
	}
	if !fexists(filepath.Join(target, "skills", "keep", "SKILL.md")) {
		t.Error("synced skill should be present")
	}
	if !fexists(filepath.Join(target, "projects", "-local", "mine.jsonl")) {
		t.Error("local project must NOT be pruned (projects are additive)")
	}
	if rep.Roots[0].Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", rep.Roots[0].Pruned)
	}
}

func TestRestore_NoPruneByDefault(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/skills/keep/SKILL.md", "keep")
	target := t.TempDir()
	write(t, target, "skills/stale/SKILL.md", "stale")
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: m}); err != nil {
		t.Fatal(err)
	}
	if !fexists(filepath.Join(target, "skills", "stale", "SKILL.md")) {
		t.Error("without --prune, stale file must remain")
	}
}
