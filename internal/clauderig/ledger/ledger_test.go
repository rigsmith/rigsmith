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

// Attribution is ranked and sticky: ground truth may upgrade an inference, but
// nothing downgrades, and re-syncing under a different login cannot relabel a
// session someone else's.
func TestNote_AccountAttributionIsRankedAndSticky(t *testing.T) {
	base := Entry{ID: "s1", End: time.Unix(100, 0).UTC(), Bytes: 10}

	t.Run("inference is recorded when nothing is known", func(t *testing.T) {
		l, _ := Open(t.TempDir(), "mbp")
		e := base
		e.Account, e.AccountSource = "acct-a", AccountFromSync
		if !l.Note(e) {
			t.Fatal("first note should write")
		}
		if a, s := l.Attribution("s1"); a != "acct-a" || s != AccountFromSync {
			t.Errorf("got %q/%q", a, s)
		}
	})

	t.Run("a later sync under another login does not relabel", func(t *testing.T) {
		l, _ := Open(t.TempDir(), "mbp")
		e := base
		e.Account, e.AccountSource = "acct-a", AccountFromSync
		l.Note(e)
		// same rank, different account, and a changed transcript
		e2 := base
		e2.Bytes, e2.End = 20, time.Unix(200, 0).UTC()
		e2.Account, e2.AccountSource = "acct-b", AccountFromSync
		l.Note(e2)
		if a, _ := l.Attribution("s1"); a != "acct-a" {
			t.Errorf("sticky attribution lost: got %q, want acct-a", a)
		}
	})

	t.Run("desktop ground truth upgrades an inference", func(t *testing.T) {
		l, _ := Open(t.TempDir(), "mbp")
		e := base
		e.Account, e.AccountSource = "acct-a", AccountFromSync
		l.Note(e)
		up := base // byte-identical transcript: only the attribution improves
		up.Account, up.AccountSource = "acct-b", AccountFromDesktop
		if !l.Note(up) {
			t.Fatal("an account upgrade must write even when the transcript is unchanged")
		}
		if a, s := l.Attribution("s1"); a != "acct-b" || s != AccountFromDesktop {
			t.Errorf("got %q/%q, want acct-b/%s", a, s, AccountFromDesktop)
		}
	})

	t.Run("ground truth is never downgraded", func(t *testing.T) {
		l, _ := Open(t.TempDir(), "mbp")
		e := base
		e.Account, e.AccountSource = "acct-b", AccountFromDesktop
		l.Note(e)
		down := base
		down.Bytes, down.End = 20, time.Unix(200, 0).UTC()
		down.Account, down.AccountSource = "acct-a", AccountFromSync
		l.Note(down)
		if a, s := l.Attribution("s1"); a != "acct-b" || s != AccountFromDesktop {
			t.Errorf("ground truth downgraded to %q/%q", a, s)
		}
	})

	t.Run("an unattributed row stays writable and takes the first account", func(t *testing.T) {
		l, _ := Open(t.TempDir(), "mbp")
		l.Note(base)
		if a, _ := l.Attribution("s1"); a != "" {
			t.Fatalf("expected no attribution, got %q", a)
		}
		later := base
		later.Account, later.AccountSource = "acct-a", AccountFromSync
		if !l.Note(later) {
			t.Fatal("adding a first attribution must write")
		}
		if a, _ := l.Attribution("s1"); a != "acct-a" {
			t.Errorf("got %q", a)
		}
	})
}

// Note() ranks attribution within one device's file; the union is where two
// files meet. Without ranking there too, a machine that later re-saw the same
// transcript with no sidecar would replace another machine's Desktop ground
// truth purely by having synced last.
func TestLoadAll_KeepsGroundTruthAcrossDevices(t *testing.T) {
	dir := t.TempDir()
	end := time.Unix(100, 0).UTC()

	// Machine A: ground truth, seen earlier.
	a, _ := Open(dir, "machine-a")
	a.Note(Entry{ID: "s1", End: end, Bytes: 10, Seen: time.Unix(500, 0).UTC(),
		Account: "acct-truth", AccountSource: AccountFromDesktop})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	// Machine B: same transcript, no sidecar, synced LATER.
	b, _ := Open(dir, "machine-b")
	b.Note(Entry{ID: "s1", End: end, Bytes: 10, Seen: time.Unix(900, 0).UTC(),
		Account: "acct-guess", AccountSource: AccountFromSync})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadAll(dir)["s1"]
	if got.Account != "acct-truth" || got.AccountSource != AccountFromDesktop {
		t.Errorf("union = %q/%q, want acct-truth/%s — recency must not outrank ground truth",
			got.Account, got.AccountSource, AccountFromDesktop)
	}
	// The rest of the row still comes from the newer sighting.
	if !got.Seen.Equal(time.Unix(900, 0).UTC()) {
		t.Errorf("Seen = %v, want the newer row's", got.Seen)
	}
}

// An older twin that only contributes a better attribution is still a rewrite,
// so its Seen stamp has to move — it is a tiebreak in the union.
func TestNote_OlderTwinUpgradeStampsSeen(t *testing.T) {
	l, _ := Open(t.TempDir(), "mbp")
	newer := Entry{ID: "s1", End: time.Unix(200, 0).UTC(), Bytes: 20,
		Seen: time.Unix(500, 0).UTC(), Account: "a", AccountSource: AccountFromSync}
	l.Note(newer)

	olderWithTruth := Entry{ID: "s1", End: time.Unix(100, 0).UTC(), Bytes: 10,
		Seen: time.Unix(900, 0).UTC(), Account: "b", AccountSource: AccountFromDesktop}
	if !l.Note(olderWithTruth) {
		t.Fatal("an attribution upgrade from the older twin must write")
	}
	got := LoadAll(mustSave(t, l))["s1"]
	if got.AccountSource != AccountFromDesktop || got.Account != "b" {
		t.Errorf("attribution = %q/%q, want b/%s", got.Account, got.AccountSource, AccountFromDesktop)
	}
	if !got.End.Equal(time.Unix(200, 0).UTC()) || got.Bytes != 20 {
		t.Errorf("the older twin overwrote transcript state: end=%v bytes=%d", got.End, got.Bytes)
	}
	if !got.Seen.Equal(time.Unix(900, 0).UTC()) {
		t.Errorf("Seen = %v, want the writing row's stamp", got.Seen)
	}
}

func mustSave(t *testing.T, l *Ledger) string {
	t.Helper()
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	return l.dir
}

// Two devices, both with only a sync-rank guess: the FIRST attribution owns the
// session. Ranking alone is not enough — mergeAccount keeps its first argument
// on a tie, and the union's first argument is the newer row, so a second
// machine recording an already-staged transcript under its own live login would
// otherwise relabel it on a routine sync.
func TestLoadAll_EqualRankKeepsTheFirstAttribution(t *testing.T) {
	dir := t.TempDir()
	end := time.Unix(100, 0).UTC()

	first, _ := Open(dir, "machine-a")
	first.Note(Entry{ID: "s1", End: end, Bytes: 10, Seen: time.Unix(500, 0).UTC(),
		Account: "acct-first", AccountSource: AccountFromSync})
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	later, _ := Open(dir, "machine-b")
	later.Note(Entry{ID: "s1", End: end, Bytes: 10, Seen: time.Unix(900, 0).UTC(),
		Account: "acct-later", AccountSource: AccountFromSync})
	if err := later.Save(); err != nil {
		t.Fatal(err)
	}

	if got := LoadAll(dir)["s1"]; got.Account != "acct-first" {
		t.Errorf("union = %q, want acct-first — a routine sync must not relabel", got.Account)
	}
}

// Three devices, all equal rank, sightings at 500 / 900 / 700. The first
// attribution must win outright. Tying on Seen could not do this: merging the
// 500 and 900 rows produces a row carrying the NEWER transcript's Seen (900)
// beside the OLDER row's account, so the 700 row would look earlier than the
// merged winner and take a session it never attributed first.
func TestLoadAll_ThreeDeviceEqualRankKeepsTheEarliestAttribution(t *testing.T) {
	dir := t.TempDir()
	end := time.Unix(100, 0).UTC()
	write := func(device, account string, seen int64) {
		l, _ := Open(dir, device)
		l.Note(Entry{ID: "s1", End: end, Bytes: 10, Seen: time.Unix(seen, 0).UTC(),
			Account: account, AccountSource: AccountFromSync})
		if err := l.Save(); err != nil {
			t.Fatal(err)
		}
	}
	write("machine-a", "acct-first", 500)
	write("machine-b", "acct-later", 900)
	write("machine-c", "acct-middle", 700)

	got := LoadAll(dir)["s1"]
	if got.Account != "acct-first" {
		t.Errorf("union = %q, want acct-first (earliest attribution)", got.Account)
	}
	if !got.AccountSince.Equal(time.Unix(500, 0).UTC()) {
		t.Errorf("AccountSince = %v, want the first attribution's stamp", got.AccountSince)
	}
}

// AccountSince must not move when the transcript changes, or a machine merely
// re-syncing an updated transcript would push its stamp past another device's
// and take the session.
func TestNote_AccountSinceSurvivesATranscriptUpdate(t *testing.T) {
	l, _ := Open(t.TempDir(), "mbp")
	l.Note(Entry{ID: "s1", End: time.Unix(100, 0).UTC(), Bytes: 10,
		Seen: time.Unix(500, 0).UTC(), Account: "acct-a", AccountSource: AccountFromSync})

	// same account, later write, bigger transcript
	l.Note(Entry{ID: "s1", End: time.Unix(200, 0).UTC(), Bytes: 20,
		Seen: time.Unix(900, 0).UTC(), Account: "acct-a", AccountSource: AccountFromSync})

	got := l.rows["s1"]
	if !got.AccountSince.Equal(time.Unix(500, 0).UTC()) {
		t.Errorf("AccountSince = %v, want it pinned to the first attribution (500)", got.AccountSince)
	}
	if !got.Seen.Equal(time.Unix(900, 0).UTC()) {
		t.Errorf("Seen = %v, want it to track the latest write", got.Seen)
	}
}

// A legacy row has Account and AccountSource but no AccountSince. A zero time
// precedes everything, so without a fallback any later equal-rank attribution
// would beat it — quietly handing every historical session to whichever machine
// synced next.
func TestLoadAll_LegacyRowKeepsItsAttribution(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	// written by hand, exactly as an older clauderig left it: no accountSince
	legacy := `{"id":"s1","end":"2026-08-01T00:00:00Z","bytes":10,"seen":"2026-08-01T00:00:00Z","account":"acct-legacy","accountSource":"sync"}`
	if err := os.WriteFile(filepath.Join(dir, DirName, "machine-a.jsonl"), []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	later, _ := Open(dir, "machine-b")
	later.Note(Entry{ID: "s1", End: mustTime(t, "2026-08-01T00:00:00Z"), Bytes: 10,
		Seen: mustTime(t, "2026-08-20T00:00:00Z"), Account: "acct-new", AccountSource: AccountFromSync})
	if err := later.Save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadAll(dir)["s1"]; got.Account != "acct-legacy" {
		t.Errorf("union = %q, want acct-legacy — a legacy row must not lose to a newer stamp", got.Account)
	}
}

// Two machines whose clocks disagree can produce identical stamps. Wall-clock
// order cannot say which came first, so the tie must at least be resolved the
// SAME way everywhere — otherwise the union disagrees between devices.
func TestBestAccount_IdenticalStampsResolveDeterministically(t *testing.T) {
	at := mustTime(t, "2026-08-01T00:00:00Z")
	a := Entry{ID: "s1", Account: "bbb", AccountSource: AccountFromSync, AccountSince: at}
	b := Entry{ID: "s1", Account: "aaa", AccountSource: AccountFromSync, AccountSince: at}

	got1, _, _ := bestAccount(a, b)
	got2, _, _ := bestAccount(b, a)
	if got1 != got2 {
		t.Errorf("order-dependent: %q vs %q", got1, got2)
	}
	if got1 != "aaa" {
		t.Errorf("got %q, want the deterministic winner", got1)
	}
}

// A row written by a NEWER clauderig must survive a round trip through this one
// rather than losing its unknown fields on the next rewrite.
func TestReadWrite_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	row := `{"id":"s1","end":"2026-08-01T00:00:00Z","bytes":10,"seen":"2026-08-01T00:00:00Z","futureField":"keep me"}`
	path := filepath.Join(dir, DirName, "mbp.jsonl")
	if err := os.WriteFile(path, []byte(row+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Open(dir, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	l.Note(Entry{ID: "s1", End: mustTime(t, "2026-08-02T00:00:00Z"), Bytes: 20,
		Seen: mustTime(t, "2026-08-02T00:00:00Z")})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "futureField") {
		t.Errorf("unknown field dropped on rewrite: %s", b)
	}
}

func mustTime(t *testing.T, v string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatal(err)
	}
	return ts.UTC()
}
