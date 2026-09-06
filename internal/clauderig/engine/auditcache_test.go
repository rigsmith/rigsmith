package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// A cache from a previous version that called the file clean.
	stale := "clauderig-audit 0\n" + "0 0 cli/projects/-p/a.jsonl\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(stage), ".audit-cache"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if found, err := Audit(stage); err != nil || len(found) != 1 {
		t.Fatalf("stale cache was trusted: %v %v", found, err)
	}
}
