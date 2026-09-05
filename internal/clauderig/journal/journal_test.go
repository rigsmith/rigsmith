package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()

	if err := Append(dir, Record{Machine: "air", Op: OpSync, Outcome: OutcomeOK, Files: 12}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Record{Machine: "air", Op: OpPull, Outcome: OutcomeOK}); err != nil {
		t.Fatal(err)
	}

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	// Newest first.
	if recs[0].Op != OpPull {
		t.Errorf("expected the pull first, got %+v", recs[0])
	}
	if recs[1].Files != 12 {
		t.Errorf("lost the file count: %+v", recs[1])
	}
	// At is stamped even though the caller left it zero.
	if recs[0].At.IsZero() {
		t.Error("At was not stamped")
	}
}

// Reading a staging dir that has never been journalled is a normal state (every
// machine, before its first sync), not an error.
func TestReadMissingDirIsEmpty(t *testing.T) {
	recs, err := Read(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("missing journal should read empty, got %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records from an empty dir", len(recs))
	}
}

// The whole point of one file per machine: two machines' records merge into one
// feed without either writing the other's file.
func TestReadMergesMachines(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	mustAppend(t, dir, Record{Machine: "pro", Op: OpSync, At: base})
	mustAppend(t, dir, Record{Machine: "air", Op: OpSync, At: base.Add(time.Minute)})
	mustAppend(t, dir, Record{Machine: "pro", Op: OpPull, At: base.Add(2 * time.Minute)})

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	wantOrder := []string{"pro", "air", "pro"}
	for i, want := range wantOrder {
		if recs[i].Machine != want {
			t.Errorf("record %d machine = %q, want %q", i, recs[i].Machine, want)
		}
	}

	// Each machine wrote only its own file.
	entries, _ := os.ReadDir(filepath.Join(dir, DirName))
	if len(entries) != 2 {
		t.Fatalf("want 2 journal files, got %d", len(entries))
	}
}

func TestReadLimit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	for i := range 10 {
		mustAppend(t, dir, Record{Machine: "air", Op: OpSync, At: base.Add(time.Duration(i) * time.Minute)})
	}

	recs, err := Read(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	// The limit must keep the newest, not the first three written.
	if !recs[0].At.Equal(base.Add(9 * time.Minute)) {
		t.Errorf("limit dropped the newest record: got %v", recs[0].At)
	}
}

// A torn or hand-edited line costs one row, never the whole feed.
func TestReadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	mustAppend(t, dir, Record{Machine: "air", Op: OpSync, Files: 7})

	path := filepath.Join(dir, DirName, "air.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"at\": truncated-mid-w\n\n")
	f.Close()

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Files != 7 {
		t.Fatalf("good record lost to a bad neighbour: %+v", recs)
	}
}

// The journal must never be the thing that wedges a sync — oversized files have
// already done that once.
func TestCompactBoundsTheFile(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	for i := range MaxRecords + 25 {
		mustAppend(t, dir, Record{Machine: "air", Op: OpSync, At: base.Add(time.Duration(i) * time.Second)})
	}

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != MaxRecords {
		t.Fatalf("got %d records, want the cap of %d", len(recs), MaxRecords)
	}
	// Compaction drops the oldest, keeps the newest.
	if !recs[0].At.Equal(base.Add(time.Duration(MaxRecords+24) * time.Second)) {
		t.Errorf("newest record lost to compaction: %v", recs[0].At)
	}
	if got := recs[len(recs)-1].At; got.Before(base.Add(25 * time.Second)) {
		t.Errorf("oldest kept record %v is older than expected after trimming", got)
	}
	// No temp file left behind.
	if _, err := os.Stat(filepath.Join(dir, DirName, "air.jsonl.tmp")); !os.IsNotExist(err) {
		t.Error("compaction left its temp file behind")
	}
}

// A Stop-hook sync and a hand-run sync can land at once; neither may produce a
// half-written line that costs the other its record.
func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	const n = 40

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = Append(dir, Record{Machine: "air", Op: OpSync, Files: i})
		}(i)
	}
	wg.Wait()

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != n {
		t.Fatalf("got %d records from %d concurrent appends — a write was torn or lost", len(recs), n)
	}
}

// The machine name reaches us from config or the OS hostname, so it must never
// be able to steer the write out of the journal directory.
func TestFileNameIsASafeSegment(t *testing.T) {
	tests := map[string]string{
		"Johns-MacBook-Pro16": "Johns-MacBook-Pro16.jsonl",
		"../../etc/passwd":    "etc-passwd.jsonl",
		"a/b":                 "a-b.jsonl",
		`win\path`:            "win-path.jsonl",
		"":                    "unknown.jsonl",
		"...":                 "unknown.jsonl",
		"name with spaces":    "name-with-spaces.jsonl",
	}
	for in, want := range tests {
		got := fileName(in)
		if got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("fileName(%q) = %q escapes its directory", in, got)
		}
	}
}

// A traversal-shaped name must write inside the journal dir, not above it.
func TestAppendConfinesTraversalNames(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{Machine: "../../escape", Op: OpSync}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one file in the journal dir, got %d", len(entries))
	}
	// dir itself should hold only the journal directory.
	top, _ := os.ReadDir(dir)
	if len(top) != 1 || top[0].Name() != DirName {
		t.Fatalf("Append wrote outside the journal dir: %v", top)
	}
}

// Git errors are multi-line; a record is one JSONL row, so the text is folded.
func TestErrorIsFoldedToOneLine(t *testing.T) {
	dir := t.TempDir()
	err := errors.New("push rejected\n  hint: updates were rejected\n  hint: fetch first")
	mustAppend(t, dir, Failed("air", OpSync, err))

	recs, _ := Read(dir, 0)
	if strings.ContainsAny(recs[0].Error, "\n\r") {
		t.Fatalf("error text kept its newlines: %q", recs[0].Error)
	}
	if !strings.Contains(recs[0].Error, "push rejected") {
		t.Fatalf("error text lost its meaning: %q", recs[0].Error)
	}
}

func TestFromSyncSumsRoots(t *testing.T) {
	rep := &engine.Report{
		ManifestProjects: 42,
		RetentionPruned:  3,
		Roots: []engine.RootResult{
			{ID: "cli", Files: 100, Redactions: 4, RetentionByAge: 2, SkippedFiles: 1, Oversize: []string{"a", "b"}},
			{ID: "desktop", Files: 20, Redactions: 1},
			{ID: "absent", Files: 999, Skipped: true}, // must not count
		},
	}

	rec := FromSync("air", rep, nil)
	if rec.Files != 120 {
		t.Errorf("Files = %d, want 120 (skipped root excluded)", rec.Files)
	}
	if rec.Redactions != 5 {
		t.Errorf("Redactions = %d, want 5", rec.Redactions)
	}
	// The two retention numbers mean different things and must not be summed:
	// AgedOut is a deletion that happened, TooOld is a standing property of the
	// live tree that repeats on every run forever.
	if rec.AgedOut != 3 {
		t.Errorf("AgedOut = %d, want 3 (pruned from staging only)", rec.AgedOut)
	}
	if rec.TooOld != 2 {
		t.Errorf("TooOld = %d, want 2 (declined at the source)", rec.TooOld)
	}
	if rec.Oversize != 2 || rec.Skipped != 1 || rec.Projects != 42 {
		t.Errorf("unexpected counts: %+v", rec)
	}
	if !rec.OK() {
		t.Errorf("outcome = %q, want ok", rec.Outcome)
	}
}

// The tripwire is a safety property doing its job, not a malfunction — it has
// to read differently from a crash, or people learn to ignore it. This is the
// exact case that ran silently for days in July 2026.
func TestFromSyncTripwireIsRefusedNotFailed(t *testing.T) {
	rep := &engine.Report{
		Findings: []redact.Finding{
			{Path: "env.ANTHROPIC_API_KEY", Kind: "anthropic-key"},
			{Path: "mcp.token", Kind: "jwt"},
		},
	}
	rec := FromSync("air", rep, errors.New("secret tripwire: 2 value(s) look like credentials"))

	if rec.Outcome != OutcomeRefused {
		t.Fatalf("outcome = %q, want %q", rec.Outcome, OutcomeRefused)
	}
	if len(rec.Leaks) != 2 || rec.Leaks[0].Kind != "anthropic-key" {
		t.Fatalf("leaks not carried through: %+v", rec.Leaks)
	}
}

// A genuine error with no findings stays Failed.
func TestFromSyncErrorWithoutLeaksIsFailed(t *testing.T) {
	rec := FromSync("air", &engine.Report{}, errors.New("disk full"))
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
}

func TestFromSyncNilReport(t *testing.T) {
	rec := FromSync("air", nil, errors.New("boom"))
	if rec.Outcome != OutcomeFailed || rec.Machine != "air" || rec.Op != OpSync {
		t.Fatalf("unexpected record: %+v", rec)
	}
}

func TestFromRestore(t *testing.T) {
	rep := &engine.RestoreReport{Roots: []engine.RestoreRootResult{
		{ID: "cli", Files: 30}, {ID: "desktop", Files: 5},
	}}
	rec := FromRestore("air", rep, nil)
	if rec.Op != OpRestore || rec.Files != 35 || !rec.OK() {
		t.Fatalf("unexpected record: %+v", rec)
	}
}

func mustAppend(t *testing.T, dir string, rec Record) {
	t.Helper()
	if err := Append(dir, rec); err != nil {
		t.Fatal(err)
	}
}

// Two records stamped inside the same clock tick — which on Windows is a 15ms
// window two syncs in a row land inside routinely — still read newest-first.
// The file is append-only, so its line order is the only thing left to order
// them by.
func TestRead_SameTimestampKeepsAppendOrder(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	if err := Append(dir, Record{At: at, Machine: "air", Op: OpSync, Outcome: OutcomeOK}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Record{At: at, Machine: "air", Op: OpPull, Outcome: OutcomeOK}); err != nil {
		t.Fatal(err)
	}

	recs, err := Read(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Op != OpPull {
		t.Errorf("got %+v, want the later append first", recs)
	}
}
