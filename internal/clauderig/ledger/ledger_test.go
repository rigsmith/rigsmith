package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLedger_NoteFreshSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	l, err := Open(dir, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if l.Fresh("s1", t0, 10) {
		t.Error("an unknown session can't be fresh")
	}
	if !l.Note(Entry{ID: "s1", Title: "auth refactor", Cwd: "/Users/j/Git/api", End: t0, Bytes: 10, Seen: t0}) {
		t.Error("first Note should write")
	}
	// Same fingerprint → no rewrite, which is what keeps a steady-state sync from
	// touching the file at all.
	if !l.Fresh("s1", t0, 10) {
		t.Error("same size+mtime should read as fresh")
	}
	if l.Note(Entry{ID: "s1", End: t0, Bytes: 10, Seen: t0}) {
		t.Error("unchanged Note should not write")
	}
	// A grown transcript is a changed fingerprint.
	if !l.Note(Entry{ID: "s1", Title: "auth refactor", End: t0.Add(time.Hour), Bytes: 99, Seen: t0}) {
		t.Error("changed fingerprint should rewrite")
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadAll(dir)
	if len(got) != 1 || got["s1"].Bytes != 99 || got["s1"].Title != "auth refactor" {
		t.Fatalf("LoadAll = %+v", got)
	}
	// RecordedBy is stamped by the ledger, not by the caller: it names the machine
	// whose sync wrote the row.
	if got["s1"].RecordedBy != "mbp" {
		t.Errorf("RecordedBy = %q, want mbp", got["s1"].RecordedBy)
	}
}

// The point of the ledger: a row outlives the transcript it describes.
func TestLedger_RowsAreNeverRemoved(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	l, _ := Open(dir, "mbp")
	l.Note(Entry{ID: "old", Title: "march planning", End: t0, Bytes: 5, Seen: t0})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	// A later sync sees only newer sessions — the old transcript aged out.
	l2, _ := Open(dir, "mbp")
	l2.Note(Entry{ID: "new", Title: "august planning", End: t0.AddDate(0, 5, 0), Bytes: 7, Seen: t0})
	if err := l2.Save(); err != nil {
		t.Fatal(err)
	}
	got := LoadAll(dir)
	if _, ok := got["old"]; !ok {
		t.Fatalf("aged-out session must survive in the ledger: %+v", got)
	}
	if len(got) != 2 {
		t.Errorf("want both rows, got %d", len(got))
	}
}

// Two devices write separate files (no shared-file merge conflict) and read as
// one set, newest row per id winning.
func TestLoadAll_UnionsDevicesNewestWins(t *testing.T) {
	dir := t.TempDir()
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	a, _ := Open(dir, "air")
	a.Note(Entry{ID: "shared", Title: "stale copy", End: early, Bytes: 1, Seen: early})
	a.Note(Entry{ID: "air-only", Title: "air chat", End: early, Bytes: 1, Seen: early})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := Open(dir, "mbp")
	b.Note(Entry{ID: "shared", Title: "fresh copy", End: late, Bytes: 2, Seen: late})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadAll(dir)
	if got["shared"].Title != "fresh copy" {
		t.Errorf("newest row should win: %+v", got["shared"])
	}
	if got["air-only"].Title != "air chat" {
		t.Errorf("other device's rows should be visible: %+v", got)
	}
	if n := len(mustFiles(t, filepath.Join(dir, DirName))); n != 2 {
		t.Errorf("want one file per device, got %d", n)
	}
}

// A half-written line costs its own row and nothing else.
func TestLoadAll_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"good","title":"kept","end":"2026-03-04T12:00:00Z"}` + "\n" +
		`{"id":"trunc","title":"los` + "\n" +
		`{"title":"no id"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, DirName, "mbp.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadAll(dir)
	if len(got) != 1 || got["good"].Title != "kept" {
		t.Fatalf("want only the good row, got %+v", got)
	}
}

// An absent ledger is not an error — search must still run on a machine that has
// never synced.
func TestLoadAll_AbsentIsEmpty(t *testing.T) {
	if got := LoadAll(t.TempDir()); len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func mustFiles(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	e, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// The same session id under two slugs (a worktree copy, or a slug rewritten by
// restore) must settle on the newest row instead of the two overwriting each
// other on every sync.
func TestLedger_OlderTwinIsIgnored(t *testing.T) {
	dir := t.TempDir()
	newer := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-48 * time.Hour)

	l, _ := Open(dir, "mbp")
	if !l.Note(Entry{ID: "twin", Slug: "-a", Title: "live copy", End: newer, Bytes: 200, Seen: newer}) {
		t.Fatal("first row should write")
	}
	if l.Note(Entry{ID: "twin", Slug: "-b", Title: "stale copy", End: older, Bytes: 100, Seen: newer}) {
		t.Error("an older twin should not overwrite the newest row")
	}
	if got := l.rows["twin"].Title; got != "live copy" {
		t.Errorf("kept row = %q, want the newest", got)
	}
	// And a genuine update still lands.
	if !l.Note(Entry{ID: "twin", Slug: "-a", Title: "live copy", End: newer.Add(time.Hour), Bytes: 300, Seen: newer}) {
		t.Error("a grown transcript should still update the row")
	}
}

// The machine name comes from config.json or the hostname, so it is not
// guaranteed to be a safe filename component. A name with a separator must not
// put the file outside index/, where LoadAll never looks — that would leave the
// machine's ledger silently unread rather than failing.
func TestLedger_DeviceNameIsMadeFilenameSafe(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)

	l, err := Open(dir, "../evil name/x")
	if err != nil {
		t.Fatal(err)
	}
	l.Note(Entry{ID: "s1", Title: "kept", End: when, Bytes: 1, Seen: when})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	// Written inside index/, and readable from there.
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want one file inside %s, got %v (%v)", DirName, entries, err)
	}
	if name := entries[0].Name(); strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		t.Errorf("unsafe filename %q", name)
	}
	got := LoadAll(dir)
	if got["s1"].Title != "kept" {
		t.Fatalf("row not readable back: %+v", got)
	}
	// The real machine name survives in the row, which is what identifies it.
	if got["s1"].RecordedBy != "../evil name/x" {
		t.Errorf("RecordedBy = %q, want the original name", got["s1"].RecordedBy)
	}
}

// Two devices recording the same session settle on the row describing the LATER
// session, not the one written most recently: a machine syncing an older copy of
// a transcript today must not walk the session's date backwards.
func TestLoadAll_PrefersTheLaterSessionNotTheLaterWrite(t *testing.T) {
	dir := t.TempDir()
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// A saw the session continue (End late), and recorded it yesterday.
	a, _ := Open(dir, "a")
	a.Note(Entry{ID: "s", Title: "the long version", End: late, Bytes: 900, Seen: early})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	// B holds an older copy and records it today.
	b, _ := Open(dir, "b")
	b.Note(Entry{ID: "s", Title: "the short version", End: early, Bytes: 100, Seen: late})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadAll(dir)
	if got["s"].Title != "the long version" || !got["s"].End.Equal(late) {
		t.Errorf("a later write of an older session won: %+v", got["s"])
	}
}
