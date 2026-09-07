package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

func fexists(p string) bool { _, err := os.Stat(p); return err == nil }

// An unparseable .json must be skipped, never synced raw (it can't be redacted or
// scanned — syncing it would defeat the secrets guarantee).
func TestSync_InvalidJSONSkippedNotRaw(t *testing.T) {
	live := t.TempDir()
	write(t, live, "settings.json", `{ not valid json, sk-ant-SECRET12345678`)
	write(t, live, "skills/a/SKILL.md", "ok")
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if fexists(filepath.Join(staging, "cli", "settings.json")) {
		t.Error("invalid JSON should be skipped, not synced raw")
	}
	if !fexists(filepath.Join(staging, "cli", "skills", "a", "SKILL.md")) {
		t.Error("valid files should still sync")
	}
	if rep.Roots[0].SkippedFiles == 0 {
		t.Error("expected the skipped invalid file to be counted")
	}
}

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

	if _, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)}); err != nil {
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

	rep, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)})
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
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)})
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

// Memory is durable state, not a dated record: an aged memory file still syncs
// while an equally aged transcript beside it is dropped.
func TestSync_RetentionKeepsAgedMemory(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-p/old.jsonl", "old session")
	write(t, live, "projects/-p/memory/MEMORY.md", "- [Old fact](old-fact.md) — hook\n")
	write(t, live, "projects/-p/memory/old-fact.md", "a fact that hasn't changed in months")
	old := time.Now().AddDate(0, 0, -40)
	for _, rel := range []string{"old.jsonl", "memory/MEMORY.md", "memory/old-fact.md"} {
		if err := os.Chtimes(filepath.Join(live, "projects", "-p", filepath.FromSlash(rel)), old, old); err != nil {
			t.Fatal(err)
		}
	}

	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"memory/MEMORY.md", "memory/old-fact.md"} {
		if !fexists(filepath.Join(staging, "cli", "projects", "-p", filepath.FromSlash(rel))) {
			t.Errorf("aged memory file %s should be exempt from retention", rel)
		}
	}
	if fexists(filepath.Join(staging, "cli", "projects", "-p", "old.jsonl")) {
		t.Error("aged transcript should still be dropped by retention")
	}
	if rep.Roots[0].RetentionByAge != 1 {
		t.Errorf("RetentionByAge = %d, want 1 (the transcript only)", rep.Roots[0].RetentionByAge)
	}
}

// The staging prune must not delete already-synced memory either — and a project
// whose transcripts have all aged out keeps its slug for the sake of its memory.
func TestSync_StagingPruneKeepsMemoryAndItsSlug(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	write(t, live, "projects/-Users-john-Git-p/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/p","isSidechain":false}`+"\n")
	write(t, live, "projects/-Users-john-Git-p/memory/fact.md", "durable")
	m := config.Machine{Name: "j", OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	if _, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)}); err != nil {
		t.Fatal(err)
	}

	// Age everything staged and drop the project locally, so only the prune decides.
	staged := filepath.Join(staging, "cli", "projects", "-Users-john-Git-p")
	old := time.Now().AddDate(0, 0, -40)
	for _, rel := range []string{"s.jsonl", "memory/fact.md"} {
		if err := os.Chtimes(filepath.Join(staged, filepath.FromSlash(rel)), old, old); err != nil {
			t.Fatal(err)
		}
	}
	os.RemoveAll(filepath.Join(live, "projects", "-Users-john-Git-p"))

	if _, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, RetentionDays: 30, SourceOverride: override("cli", live)}); err != nil {
		t.Fatal(err)
	}
	if !fexists(filepath.Join(staged, "memory", "fact.md")) {
		t.Error("memory should survive the staging prune regardless of age")
	}
	if fexists(filepath.Join(staged, "s.jsonl")) {
		t.Error("aged transcript should be pruned from staging")
	}
}

func TestSync_IncrementalSkipsUnchanged(t *testing.T) {
	live := t.TempDir()
	write(t, live, "skills/a/SKILL.md", "body")
	write(t, live, "projects/-p/s.jsonl", "transcript")
	// Written before the first run rather than during it. A file stamped in the
	// same tick as the run reading it is ambiguous by construction — a rewrite
	// inside that tick carries the same stamp — so sync restages it, and this
	// is a test about the files it does not restage.
	settle(t, live, "skills/a/SKILL.md", "projects/-p/s.jsonl")
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	r1, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Roots[0].Unchanged != 0 {
		t.Errorf("first sync Unchanged = %d, want 0", r1.Roots[0].Unchanged)
	}

	r2, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
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
		TargetOverride: override("cli", target),
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
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: m, TargetOverride: override("cli", target)}); err != nil {
		t.Fatal(err)
	}
	if !fexists(filepath.Join(target, "skills", "stale", "SKILL.md")) {
		t.Error("without --prune, stale file must remain")
	}
}

// JSON is regenerated on every sync — read, redacted, portablized, re-marshalled
// — so unlike the plain-copy path it can't be skipped on mtime. It was therefore
// counted as written every single time, which made Files a constant floor rather
// than a measure of change and left the activity feed repeating one identical
// line forever. The comparison happens on the produced bytes instead.
func TestSync_UnchangedJSONIsNotCountedAsWritten(t *testing.T) {
	live := t.TempDir()
	write(t, live, "settings.json", `{"theme":"dark"}`)
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	r1, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Roots[0].Files != 1 {
		t.Fatalf("first sync Files = %d, want 1", r1.Roots[0].Files)
	}

	r2, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Roots[0].Files != 0 {
		t.Errorf("second sync Files = %d, want 0 — nothing changed", r2.Roots[0].Files)
	}
	if r2.Roots[0].Unchanged != 1 {
		t.Errorf("second sync Unchanged = %d, want 1", r2.Roots[0].Unchanged)
	}

	// And a real edit still gets through.
	write(t, live, "settings.json", `{"theme":"light"}`)
	r3, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r3.Roots[0].Files != 1 {
		t.Errorf("edited JSON: Files = %d, want 1", r3.Roots[0].Files)
	}
	got, err := os.ReadFile(filepath.Join(staging, "cli", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "light") {
		t.Errorf("staged copy did not pick up the edit: %s", got)
	}
}

// "21 secrets redacted" appeared on every row of the activity feed, beside
// syncs that had written a single file. Every JSON file is redacted on every
// pass — that is how the JSON path works — so counting at redaction time
// reported the whole tree's secrets as though they were this run's. The count
// belongs to the files actually staged.
func TestSync_RedactionsCountOnlyWhatWasWritten(t *testing.T) {
	live := t.TempDir()
	write(t, live, "settings.json", `{"env":{"API_KEY":"sk-ant-api03-`+strings.Repeat("x", 40)+`"}}`)
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)

	r1, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Roots[0].Redactions == 0 {
		t.Fatal("first sync redacted nothing — the fixture is not exercising the redactor")
	}

	// Nothing changed, so nothing was staged, so this run redacted nothing —
	// even though the redactor ran over the same file again to find that out.
	r2, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Roots[0].Redactions != 0 {
		t.Errorf("second sync Redactions = %d, want 0 — it staged no files", r2.Roots[0].Redactions)
	}
}

// A key pasted into a conversation was never examined: the content rules stop at
// 64 KB and every real transcript is bigger, so it reached the synced repo
// verbatim. With redaction on, the staged copy is scrubbed and the live file is
// left exactly as it was — clauderig backs a machine up, it does not edit it.
func TestSync_RedactsTranscriptsWithoutTouchingTheLiveFile(t *testing.T) {
	live := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("z", 60)
	// Padded past the content-scan limit, which is what a real transcript is.
	var body strings.Builder
	body.WriteString(`{"type":"user","cwd":"/p","text":"my key is ` + key + `"}` + "\n")
	for body.Len() < 80<<10 {
		body.WriteString(`{"type":"assistant","text":"` + strings.Repeat("filler ", 40) + `"}` + "\n")
	}
	write(t, live, "projects/-p/s.jsonl", body.String())

	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)
	opts := Options{StagingDir: staging, Config: cfg, Machine: m,
		RedactTranscripts: true, SourceOverride: override("cli", live)}

	rep, err := Sync(opts)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(staging, "cli", "projects", "-p", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(staged), key) {
		t.Error("the key reached staging")
	}
	if !strings.Contains(string(staged), redact.Placeholder) {
		t.Error("nothing was redacted")
	}
	// The rest of the conversation has to come through untouched.
	if !strings.Contains(string(staged), "filler") || !strings.Contains(string(staged), `"type":"assistant"`) {
		t.Error("the transcript was damaged")
	}

	// The live file is the point: it must be exactly as the user left it.
	orig, err := os.ReadFile(filepath.Join(live, "projects", "-p", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(orig), key) {
		t.Error("the LIVE transcript was modified — clauderig must never edit ~/.claude")
	}

	// And the run says which file it was, not merely how many values.
	var found bool
	for _, r := range rep.Roots {
		for _, fr := range r.Redacted {
			if fr.Rel == "projects/-p/s.jsonl" && len(fr.Kinds) > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the redaction was counted but not attributed to a file: %+v", rep.Roots[0])
	}
}

// Off by default: rewriting the middle of somebody's conversation is not a thing
// a backup tool does uninvited.
func TestSync_RefusesSecretsWithoutRewritingLiveTranscripts(t *testing.T) {
	live := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("z", 60)
	body := `{"type":"user","text":"` + key + `"}` + "\n"
	write(t, live, "projects/-p/s.jsonl", body)
	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)})
	if err == nil || len(rep.Findings) == 0 {
		t.Fatal("expected secret refusal")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatal("error disclosed credential")
	}
	if got := read(t, filepath.Join(live, "projects/-p/s.jsonl")); got != body {
		t.Fatal("live transcript changed")
	}
}

// Turning redaction on has to reach what is already staged. Nothing about those
// transcripts changes when the setting does, so the incremental skip kept
// handing back the copy holding the key.
func TestSync_EnablingRedactionScrubsWhatIsAlreadyStaged(t *testing.T) {
	live := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("z", 60)
	var body strings.Builder
	body.WriteString(`{"type":"user","cwd":"/p","text":"my key is ` + key + `"}` + "\n")
	for body.Len() < 80<<10 {
		body.WriteString(`{"type":"assistant","text":"` + strings.Repeat("filler ", 40) + `"}` + "\n")
	}
	write(t, live, "projects/-p/s.jsonl", body.String())

	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	cfg := cliOnlyConfig(live)
	base := Options{StagingDir: staging, Config: cfg, Machine: m, SourceOverride: override("cli", live)}
	stagedPath := filepath.Join(staging, "cli", "projects", "-p", "s.jsonl")

	// Simulate an older client staging a transcript without scanning its body.
	write(t, staging, "cli/projects/-p/s.jsonl", body.String())
	st, err := os.Stat(filepath.Join(live, "projects/-p/s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Both ends stamped before this run: a mtime from the same tick as the run
	// cannot be told from one written during it, so sync restages and the
	// already-staged path under test is never reached.
	settle(t, live, "projects/-p/s.jsonl")
	st, err = os.Stat(filepath.Join(live, filepath.FromSlash("projects/-p/s.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stagedPath, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(stagedPath); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(b), key) {
		t.Fatal("the fixture did not stage the key — nothing is being tested")
	}

	on := base
	on.RedactTranscripts = true
	if _, err := Sync(on); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), key) {
		t.Error("the key staged before redaction was enabled is still there")
	}

	// And it settles: the run after that has nothing left to do.
	rep, err := Sync(on)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Roots[0].Files != 0 {
		t.Errorf("a third run restaged %d files, want none", rep.Roots[0].Files)
	}
}

// A credential lands wherever the conversation put it. Scrubbing only .jsonl
// left tool results beside the transcript carrying bearer tokens, which the
// tripwire then refused — the setting was on and the sync still would not run.
func TestSync_ScrubsToolResultsAndNotesBesideTheTranscript(t *testing.T) {
	live := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("z", 60)
	write(t, live, "projects/-p/s.jsonl", `{"type":"user","cwd":"/p","text":"my key is `+key+`"}`+"\n")
	write(t, live, "projects/-p/s/tool-results/out.txt", "Authorization: Bearer "+strings.Repeat("Aa1b2", 13)+"\n")
	write(t, live, "projects/-p/memory/notes.md", "the key was "+key+"\n")
	// Not text, and must be carried across byte for byte. It holds no
	// credential: one that did could not be scrubbed without corrupting the
	// file, so it would block the sync outright — which is the honest outcome,
	// and a different problem from this one.
	const binary = "\xff\xd8\xff\xe0 JFIF binary payload\n"
	write(t, live, "projects/-p/shot.jpg", binary)

	staging := t.TempDir()
	m := config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{
		StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
		RedactTranscripts: true, SourceOverride: override("cli", live),
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"projects/-p/s.jsonl", "projects/-p/s/tool-results/out.txt", "projects/-p/memory/notes.md"} {
		b, err := os.ReadFile(filepath.Join(staging, "cli", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if strings.Contains(string(b), key) || strings.Contains(string(b), "Aa1b2Aa1b2") {
			t.Errorf("%s still holds a credential after scrubbing", rel)
		}
	}
	// The live files are never edited — clauderig backs a machine up.
	b, err := os.ReadFile(filepath.Join(live, "projects/-p/memory/notes.md"))
	if err != nil {
		t.Fatalf("reading the live note: %v", err)
	}
	if !strings.Contains(string(b), key) {
		t.Error("the live note was rewritten; only the staged copy may be")
	}
	// A binary is copied, not rewritten.
	if b, err := os.ReadFile(filepath.Join(staging, "cli", "projects", "-p", "shot.jpg")); err != nil {
		t.Errorf("the binary did not sync: %v", err)
	} else if string(b) != binary {
		t.Error("a binary was rewritten, which is damage rather than redaction")
	}
}

// Classifying by extension gets both ends wrong. Tool output written to a .log
// or to a file with no extension is text that would keep its credential and
// keep the sync refused; a PNG somebody named .md would be handed to the
// rewriter, which would edit bytes inside an image.
func TestSync_ScrubDecidedByContentNotExtension(t *testing.T) {
	live := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("z", 60)
	write(t, live, "projects/-p/s.jsonl", `{"type":"user","cwd":"/p"}`+"\n")
	write(t, live, "projects/-p/s/tool-results/out.log", "printed "+key+"\n")
	write(t, live, "projects/-p/s/tool-results/README", "also "+key+"\n")
	const png = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR not text at all\n"
	write(t, live, "projects/-p/s/tool-results/shot.md", png)

	staging := t.TempDir()
	if _, err := Sync(Options{
		StagingDir: staging, Config: cliOnlyConfig(live),
		Machine:           config.Machine{OS: pathmap.OSMacOS, Home: "/Users/john"},
		RedactTranscripts: true, SourceOverride: override("cli", live),
	}); err != nil {
		t.Fatal(err)
	}

	// Text, whatever it is called, gets scrubbed.
	for _, rel := range []string{"projects/-p/s/tool-results/out.log", "projects/-p/s/tool-results/README"} {
		b, err := os.ReadFile(filepath.Join(staging, "cli", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if strings.Contains(string(b), key) {
			t.Errorf("%s kept its credential — it would refuse the sync with nothing left to try", rel)
		}
	}
	// A binary is carried byte for byte, whatever it is called.
	b, err := os.ReadFile(filepath.Join(staging, "cli", "projects", "-p", "s", "tool-results", "shot.md"))
	if err != nil {
		t.Fatalf("the binary did not sync: %v", err)
	}
	if string(b) != png {
		t.Error("a binary named .md was rewritten, which is damage rather than redaction")
	}
}

// The head-of-file check reads 8 KB. A file can be clean text for longer than
// that and binary after it, and rewriting a byte sequence inside the binary
// part is damage — those bytes are not a credential, they are pixels. The
// rewrite is abandoned as soon as the binary appears, and nothing half-written
// is left at the destination for the caller's plain copy to trip over.
func TestRedactTranscript_AbandonsARewriteWhenContentTurnsBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mixed.log")
	var b strings.Builder
	for b.Len() < 12<<10 {
		b.WriteString("ordinary log output, nothing to see here\n")
	}
	b.WriteString("\x00\x00 binary payload sk-ant-api03-" + strings.Repeat("z", 60) + "\n")
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "staged.log")
	_, err := redactTranscript(dst, src, time.Now())
	if !errors.Is(err, errBinaryContent) {
		t.Fatalf("err = %v, want errBinaryContent so the caller copies it instead", err)
	}
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Error("a half-rewritten file was left at the destination")
	}
}

func TestSync_UpgradesLegacyRedactionState(t *testing.T) {
	live := t.TempDir()
	staging := filepath.Join(t.TempDir(), "repo")
	key := "sk-proj-" + strings.Repeat("a", 48)
	body := "the key was " + key + "\n"
	rels := []string{"projects/-p/s/tool-results/out.txt", "projects/-p/memory/notes.md", "projects/-p/s/tool-results/out.log", "projects/-p/s/tool-results/README"}
	for _, rel := range rels {
		write(t, live, rel, body)
		write(t, staging, "cli/"+rel, body)
		st, err := os.Stat(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		settle(t, live, rel)
		if st, err = os.Stat(filepath.Join(live, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(staging, "cli", filepath.FromSlash(rel)), st.ModTime(), st.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	// The old client already had redaction on, but did not cover these files.
	if err := os.WriteFile(redactionStatePath(staging), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{StagingDir: staging, Config: cliOnlyConfig(live),
		Machine:           config.Machine{OS: pathmap.OSMacOS, Home: "/Users/fixture"},
		RedactTranscripts: true, SourceOverride: override("cli", live)}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	for _, rel := range rels {
		if got := read(t, filepath.Join(staging, "cli", filepath.FromSlash(rel))); strings.Contains(got, key) || !strings.Contains(got, redact.Placeholder) {
			t.Fatalf("%s was not scrubbed during upgrade", rel)
		}
		if got := read(t, filepath.Join(live, filepath.FromSlash(rel))); got != body {
			t.Fatalf("%s: live source changed", rel)
		}
	}
	if got := read(t, redactionStatePath(staging)); got != redactionVersion+"\n" {
		t.Fatalf("marker not upgraded: %q", got)
	}
	rep, err := Sync(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Roots[0].Files != 0 || rep.Roots[0].Redactions != 0 {
		t.Fatalf("upgrade did not settle: %+v", rep.Roots[0])
	}
}
