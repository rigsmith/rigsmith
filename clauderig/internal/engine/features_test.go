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

func TestPruneAgedStagedProjects(t *testing.T) {
	projects := t.TempDir()
	// fresh slug
	write(t, projects, "-fresh/s.jsonl", "new")
	// aged slug (set its file's mtime to the past)
	write(t, projects, "-aged/s.jsonl", "old")
	write(t, projects, "-aged/sub/tool.txt", "old")
	old := time.Now().AddDate(0, 0, -40)
	for _, f := range []string{"-aged/s.jsonl", "-aged/sub/tool.txt"} {
		os.Chtimes(filepath.Join(projects, filepath.FromSlash(f)), old, old)
	}

	cutoff := time.Now().AddDate(0, 0, -30)
	pruned, remaining, err := pruneAgedStagedProjects(projects, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}
	if fexists(filepath.Join(projects, "-aged")) {
		t.Error("aged slug dir should be removed entirely")
	}
	if !fexists(filepath.Join(projects, "-fresh", "s.jsonl")) {
		t.Error("fresh slug should remain")
	}
	if !remaining["-fresh"] || remaining["-aged"] {
		t.Errorf("remaining = %v, want only -fresh", remaining)
	}
}

// A project synced while fresh, then deleted locally and gone stale, is pruned
// from staging AND the manifest on the next sync (retention disk-prune == the
// fix for stale-slug accumulation).
func TestSync_AgedDeletedProjectPrunedFromStaging(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	write(t, live, "projects/-Users-john-Git-p/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/p","isSidechain":false}`+"\n")
	m := config.Machine{Name: "j", OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	if _, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, "cli", "projects", "-Users-john-Git-p")
	if !fexists(staged) {
		t.Fatal("project should be staged after first sync")
	}

	// Age the staged file and delete the project locally.
	old := time.Now().AddDate(0, 0, -40)
	os.Chtimes(filepath.Join(staged, "s.jsonl"), old, old)
	os.RemoveAll(filepath.Join(live, "projects", "-Users-john-Git-p"))

	rep, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if fexists(staged) {
		t.Error("aged+deleted project should be pruned from staging")
	}
	if rep.ManifestProjects != 0 {
		t.Errorf("manifest should drop the pruned project, got %d", rep.ManifestProjects)
	}
	if rep.RetentionPruned == 0 {
		t.Error("expected RetentionPruned > 0")
	}
}

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
