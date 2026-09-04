package engine

import (
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cliOnlyConfig points the cli root at a synthetic dir and disables desktop.
func cliOnlyConfig(cliDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: cliDir}},
	}
	return c
}

// override builds a root-id→dir map for Sync's SourceOverride / Restore's
// TargetOverride, taking (id, dir) pairs. These tests model a macOS Machine but
// point roots at host-native temp dirs; using the override bypasses the
// machine's path resolver (which would mangle a Windows temp path like C:\… into
// an invalid POSIX path), so the same fixtures exercise the real sync/restore
// logic on every CI OS. (See TestSync_MultiMachineManifestUnion, which already
// uses this hook.)
func override(pairs ...string) map[string]string {
	if len(pairs)%2 != 0 {
		panic("override: expected even number of (id, dir) strings")
	}
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func TestSync_RedactsCopiesAndBuildsManifest(t *testing.T) {
	live := t.TempDir()
	write(t, live, "settings.json", `{"effortLevel":"high","apiKey":"sk-ant-abcdefgh12345678"}`)
	write(t, live, "skills/x/SKILL.md", "skill body")
	write(t, live, "statsig/junk", "should not sync")
	write(t, live, "projects/-Users-john-Git-rigsmith/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/rigsmith","isSidechain":false}`+"\n")

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, ClaudeVersion: "2.1.175", SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}

	// settings.json copied + redacted
	got := read(t, filepath.Join(staging, "cli", "settings.json"))
	if !contains(got, redact.Placeholder) || contains(got, "sk-ant-") {
		t.Errorf("settings not redacted: %s", got)
	}
	// skill copied verbatim
	if read(t, filepath.Join(staging, "cli", "skills", "x", "SKILL.md")) != "skill body" {
		t.Error("skill not copied")
	}
	// junk excluded
	if _, err := os.Stat(filepath.Join(staging, "cli", "statsig", "junk")); err == nil {
		t.Error("statsig junk should not have synced")
	}
	// transcript copied
	if _, err := os.Stat(filepath.Join(staging, "cli", "projects", "-Users-john-Git-rigsmith", "s.jsonl")); err != nil {
		t.Error("transcript not copied")
	}
	// manifest built with the portable template
	if rep.ManifestProjects != 1 {
		t.Fatalf("manifest projects = %d", rep.ManifestProjects)
	}
	man := read(t, filepath.Join(staging, "clauderig-manifest.json"))
	if !contains(man, "$HOME/Git/rigsmith") {
		t.Errorf("manifest missing template: %s", man)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("unexpected tripwire findings: %v", rep.Findings)
	}
}

func TestSync_DropsOversizeFiles(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-p/small.jsonl", "{}\n")
	write(t, live, "projects/-p/marathon.jsonl", strings.Repeat("x", 4096))

	staging := t.TempDir()
	// A copy staged by an earlier, uncapped sync: the cap has to clear it, or the
	// oversized blob stays in the tree that gets committed and pushed.
	write(t, staging, "cli/projects/-p/marathon.jsonl", strings.Repeat("x", 4096))

	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{
		StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
		MaxFileBytes: 1024, SourceOverride: override("cli", live),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "cli", "projects", "-p", "marathon.jsonl")); !os.IsNotExist(err) {
		t.Error("oversize transcript should not be staged")
	}
	if _, err := os.Stat(filepath.Join(staging, "cli", "projects", "-p", "small.jsonl")); err != nil {
		t.Error("under-cap transcript should still sync")
	}
	want := []string{"projects/-p/marathon.jsonl"}
	if !reflect.DeepEqual(rep.Roots[0].Oversize, want) {
		t.Errorf("Oversize = %v, want %v", rep.Roots[0].Oversize, want)
	}
}

func TestSync_TripwireBlocksLeak(t *testing.T) {
	live := t.TempDir()
	// A secret in a DEEPER json (not field-redacted) must trip the wire.
	write(t, live, "plugins/data/leak.json", `{"saved":"ghp_aaaaaaaaaaaaaaaaaaaaa"}`)

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)})
	if err == nil {
		t.Fatal("expected tripwire error")
	}
	if len(rep.Findings) == 0 || !contains(rep.Findings[0].Path, "plugins/data/leak.json") {
		t.Fatalf("findings = %v", rep.Findings)
	}
}

func TestSync_SkipsAbsentRoot(t *testing.T) {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: "/no/such/dir/here"}},
	}
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: t.TempDir(), Config: c, Machine: m})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Roots) != 1 || !rep.Roots[0].Skipped {
		t.Fatalf("expected skipped root, got %+v", rep.Roots)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// The placeholder cleanup is gone: nothing on disk distinguishes an old
// placeholder from a legitimately empty file another machine staged, so every
// narrowing of that rule still deleted somebody's data. Reconcile judges the
// allowlist only.
func TestReconcileStagedRoot_KeepsEmptyFilesWhateverTheLivePathIs(t *testing.T) {
	staging := t.TempDir()
	empty := filepath.Join(staging, "cli", "projects", "-other", "notes")
	if err := os.MkdirAll(filepath.Dir(empty), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := reconcileStagedRoot(filepath.Join(staging, "cli"), allowlist.For("cli"))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed %d allowlisted file(s)", removed)
	}
	if _, serr := os.Stat(empty); serr != nil {
		t.Error("another machine's empty file was deleted")
	}
}

// A large, append-only transcript is restaged only per chunk of growth or once
// it has gone quiet — every sync in between would otherwise leave another
// near-identical multi-megabyte blob in history.
func TestSync_DefersLargeTranscriptsUntilGrownOrSettled(t *testing.T) {
	const threshold = 4096
	live, staging := t.TempDir(), t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	sync := func() RootResult {
		t.Helper()
		rep, err := Sync(Options{
			StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
			LargeFileBytes: threshold, SourceOverride: override("cli", live),
		})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Roots[0]
	}
	stagedSize := func() int64 {
		t.Helper()
		fi, err := os.Stat(filepath.Join(staging, "cli", "projects", "-p", "long.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}

	// First sight of a large transcript is staged whole: there is nothing to
	// defer against.
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("a", threshold+100))
	if r := sync(); r.Deferred != 0 || stagedSize() != threshold+100 {
		t.Fatalf("first sync: deferred=%d staged=%d", r.Deferred, stagedSize())
	}

	// Grown by less than half the threshold, still being written: wait.
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("a", threshold+100+threshold/4))
	if r := sync(); r.Deferred != 1 || stagedSize() != threshold+100 {
		t.Fatalf("small growth: deferred=%d staged=%d, want deferred with the old copy kept", r.Deferred, stagedSize())
	}

	// Grown by half the threshold: restage.
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("a", threshold+100+threshold/2))
	if r := sync(); r.Deferred != 0 || stagedSize() != threshold+100+threshold/2 {
		t.Fatalf("chunk growth: deferred=%d staged=%d", r.Deferred, stagedSize())
	}

	// A small tail on a transcript that has gone quiet is captured anyway — that
	// is how a finished session's last turn reaches the repo.
	src := filepath.Join(live, "projects", "-p", "long.jsonl")
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("a", threshold+100+threshold/2+10))
	old := time.Now().Add(-largeFileSettle - time.Minute)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	if r := sync(); r.Deferred != 0 || stagedSize() != threshold+100+threshold/2+10 {
		t.Fatalf("settled: deferred=%d staged=%d", r.Deferred, stagedSize())
	}

	// A transcript that SHRANK is a rewrite, not an append: never deferred.
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("b", threshold+1))
	if r := sync(); r.Deferred != 0 || stagedSize() != threshold+1 {
		t.Fatalf("shrunk: deferred=%d staged=%d", r.Deferred, stagedSize())
	}
	// So is one rewritten to the SAME size: no bytes were appended.
	write(t, live, "projects/-p/long.jsonl", strings.Repeat("c", threshold+1))
	if r := sync(); r.Deferred != 0 {
		t.Fatalf("same-size rewrite: deferred=%d, want restaged", r.Deferred)
	}
	if got, _ := os.ReadFile(filepath.Join(staging, "cli", "projects", "-p", "long.jsonl")); len(got) == 0 || got[0] != 'c' {
		t.Fatalf("same-size rewrite: staged copy not refreshed")
	}

	// Under the threshold, every change is staged as before.
	write(t, live, "projects/-p/short.jsonl", strings.Repeat("c", 100))
	sync()
	write(t, live, "projects/-p/short.jsonl", strings.Repeat("c", 101))
	if r := sync(); r.Deferred != 0 {
		t.Fatalf("small transcript deferred: %+v", r)
	}
}

// Only project transcripts are throttled: a large .jsonl anywhere else is
// ordinary data and syncs on every change.
func TestSync_DefersOnlyProjectTranscripts(t *testing.T) {
	const threshold = 4096
	live, staging := t.TempDir(), t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	sync := func() RootResult {
		t.Helper()
		rep, err := Sync(Options{
			StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
			LargeFileBytes: threshold, SourceOverride: override("cli", live),
		})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Roots[0]
	}
	write(t, live, "skills/big/data.jsonl", strings.Repeat("a", threshold+100))
	sync()
	write(t, live, "skills/big/data.jsonl", strings.Repeat("a", threshold+101))
	if r := sync(); r.Deferred != 0 {
		t.Fatalf("a skill's data file was deferred: %+v", r)
	}
	if got := read(t, filepath.Join(staging, "cli", "skills", "big", "data.jsonl")); len(got) != threshold+101 {
		t.Fatalf("staged %d bytes, want the latest %d", len(got), threshold+101)
	}
}

// A staged copy older than the retention cutoff is about to be pruned by the
// same sync; deferring the fresh source would hand retention the only copy.
func TestSync_NeverDefersACopyRetentionWouldPrune(t *testing.T) {
	const threshold = 4096
	live, staging := t.TempDir(), t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	sync := func() RootResult {
		t.Helper()
		rep, err := Sync(Options{
			StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
			LargeFileBytes: threshold, RetentionDays: 30, SourceOverride: override("cli", live),
		})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Roots[0]
	}
	src := filepath.Join(live, "projects", "-p", "old.jsonl")
	write(t, live, "projects/-p/old.jsonl", strings.Repeat("a", threshold+100))
	sync()
	// The staged copy carries the source's mtime; age both past the window,
	// then append a little to the source, as a session picking an old chat
	// back up would.
	old := time.Now().AddDate(0, 0, -40)
	staged := filepath.Join(staging, "cli", "projects", "-p", "old.jsonl")
	if err := os.Chtimes(staged, old, old); err != nil {
		t.Fatal(err)
	}
	write(t, live, "projects/-p/old.jsonl", strings.Repeat("a", threshold+110))
	_ = src
	r := sync()
	if r.Deferred != 0 {
		t.Fatalf("deferred a transcript whose staged copy retention was about to prune: %+v", r)
	}
	if got := read(t, staged); len(got) != threshold+110 {
		t.Fatalf("staged %d bytes, want the fresh %d — retention took the only copy", len(got), threshold+110)
	}
}

// A flush names the transcript of the session that ended: that one is
// restaged whatever the throttle says, and every other large transcript keeps
// waiting for its chunk — a short session ending must not restage a long
// one's 50 MB mid-chunk.
func TestSync_FlushIsScopedToTheNamedTranscript(t *testing.T) {
	const threshold = 4096
	live, staging := t.TempDir(), t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	sync := func(flush ...string) RootResult {
		t.Helper()
		rep, err := Sync(Options{
			StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
			LargeFileBytes: threshold, SourceOverride: override("cli", live), Flush: flush,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Roots[0]
	}
	stagedSize := func(name string) int64 {
		t.Helper()
		fi, err := os.Stat(filepath.Join(staging, "cli", "projects", "-p", name))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	write(t, live, "projects/-p/ended.jsonl", strings.Repeat("a", threshold+100))
	write(t, live, "projects/-p/running.jsonl", strings.Repeat("b", threshold+100))
	sync()
	write(t, live, "projects/-p/ended.jsonl", strings.Repeat("a", threshold+110))
	write(t, live, "projects/-p/running.jsonl", strings.Repeat("b", threshold+110))
	if r := sync(); r.Deferred != 2 {
		t.Fatalf("both small tails: deferred=%d, want 2", r.Deferred)
	}
	r := sync(filepath.Join(live, "projects", "-p", "ended.jsonl"))
	if r.Deferred != 1 || stagedSize("ended.jsonl") != threshold+110 || stagedSize("running.jsonl") != threshold+100 {
		t.Fatalf("flush of ended.jsonl: deferred=%d ended=%d running=%d; want the named one restaged and the other still waiting",
			r.Deferred, stagedSize("ended.jsonl"), stagedSize("running.jsonl"))
	}
}
