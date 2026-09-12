package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// The audit is the last thing between a credential and the remote, so the cache
// is only allowed to skip a file it has already read at exactly this size and
// mtime. Everything else has to be read again.
func TestAuditCacheRereadsWhatItCannotVouchFor(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "repo")
	write(t, stage, "cli/projects/-p/a.jsonl", `{"type":"user","text":"nothing here"}`+"\n")
	clean := filepath.Join(stage, "cli", "projects", "-p", "a.jsonl")

	if f, err := Audit(stage); err != nil || len(f) != 0 {
		t.Fatalf("first audit: %v %v", f, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(stage), ".audit-cache")); err != nil {
		t.Fatalf("clean audit left no cache: %v", err)
	}

	// Same file, new content and a new mtime: the verdict must not carry over.
	if err := os.WriteFile(clean, []byte(`{"type":"user","text":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(clean, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	found, err := Audit(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("cached verdict hid a credential added after it: %v", found)
	}
}

// A run that found something must not remember any of it as clean: the finding
// has to be reported again on every run until it is dealt with, and a file that
// went unread while the audit was failing was never vouched for at all.
func TestAuditCacheKeepsNothingFromARunThatFoundSomething(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "repo")
	write(t, stage, "cli/projects/-p/ok.jsonl", `{"type":"user","text":"nothing here"}`+"\n")
	write(t, stage, "cli/projects/-p/bad.jsonl", `{"type":"user","text":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n")

	if found, err := Audit(stage); err != nil || len(found) != 1 {
		t.Fatalf("audit: %v %v", found, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(stage), ".audit-cache")); !os.IsNotExist(err) {
		t.Error("a refused audit wrote a cache")
	}
	// And it still refuses on the next run rather than trusting itself.
	if found, err := Audit(stage); err != nil || len(found) != 1 {
		t.Fatalf("second audit: %v %v", found, err)
	}
}

// A cache written by an older rule set says nothing about what today's scanner
// would find, so it is discarded whole rather than trusted per file.
func TestAuditCacheIgnoresAnOlderRuleSet(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "repo")
	write(t, stage, "cli/projects/-p/a.jsonl", `{"type":"user","text":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n")

	// A cache from a previous version that called the file clean — and one that
	// carries the file's REAL size and mtime, so wasClean's own check cannot be
	// what rejects it. Written with 0 0, this test passed with the version gate
	// deleted outright: the size never matched, and the gate under test was
	// never the reason for the answer.
	rel := "cli/projects/-p/a.jsonl"
	st, err := os.Stat(filepath.Join(stage, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("clauderig-audit 0\n%d %d %s\n", st.Size(), st.ModTime().UnixNano(), rel)
	if err := os.WriteFile(filepath.Join(filepath.Dir(stage), ".audit-cache"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if found, err := Audit(stage); err != nil || len(found) != 1 {
		t.Fatalf("stale cache was trusted: %v %v", found, err)
	}
}

// The control for the test above: the same entry under the CURRENT version is
// trusted, which is what proves the version string is the only thing separating
// the two runs. Without it, a stale-cache test still passes if the entry it
// writes could never have matched anything.
func TestAuditCacheTrustsItsOwnRuleSet(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "repo")
	write(t, stage, "cli/projects/-p/a.jsonl", `{"type":"user","text":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n")

	rel := "cli/projects/-p/a.jsonl"
	st, err := os.Stat(filepath.Join(stage, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	current := fmt.Sprintf("clauderig-audit %s\n%d %d %s\n", auditVersion, st.Size(), st.ModTime().UnixNano(), rel)
	if err := os.WriteFile(filepath.Join(filepath.Dir(stage), ".audit-cache"), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	if found, err := Audit(stage); err != nil || len(found) != 0 {
		t.Fatalf("a current-version entry for this exact file was not trusted: %v %v", found, err)
	}
}

// The unchanged path may only skip its scan for bytes the audit actually read.
// A credential staged by an older clauderig — one that never scanned this file —
// has to keep failing the sync, which is the whole reason that scan exists.
func TestSyncStillScansUnchangedFilesTheAuditNeverVouchedFor(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-p/s.jsonl", `{"type":"user","text":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n")

	staging := filepath.Join(t.TempDir(), "repo")
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	opts := Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)}

	// Staged verbatim by a run with no scrubbing, as an older version would.
	if _, err := Sync(opts); err == nil {
		t.Fatal("expected the tripwire to refuse")
	}
	// Nothing changed, so the file takes the unchanged path this time. It has
	// no cached verdict (the audit never completed clean), so it must be read.
	rep, err := Sync(opts)
	if err == nil {
		t.Fatal("unchanged credential was let through on the second run")
	}
	if len(rep.Findings) == 0 {
		t.Error("no finding reported for the unchanged file")
	}
}
