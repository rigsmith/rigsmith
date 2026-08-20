package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeHistory is a git history stub: deletions it reports, blobs it can show,
// and a record of the revs it was asked for.
type fakeHistory struct {
	dels    []Deletion
	blobs   map[string]string // "rev:path" -> content
	when    time.Time
	timeErr error
	asked   []string
}

func (f *fakeHistory) Deletions(context.Context, string) ([]Deletion, error) { return f.dels, nil }

func (f *fakeHistory) LastCommitTime(_ context.Context, rev, path string) (time.Time, error) {
	return f.when, f.timeErr
}

func (f *fakeHistory) ShowPrefix(_ context.Context, rev, path string, _ int) ([]byte, error) {
	key := rev + ":" + path
	f.asked = append(f.asked, key)
	b, ok := f.blobs[key]
	if !ok {
		return nil, errors.New("no such blob")
	}
	return []byte(b), nil
}

// parseHead is a stand-in for the real transcript readers: first line is the
// title, second is the cwd.
func parseHead(head []byte) (string, string) {
	lines := strings.SplitN(string(head), "\n", 3)
	for len(lines) < 2 {
		lines = append(lines, "")
	}
	return lines[0], lines[1]
}

func TestBackfill_RecoversPrunedSessions(t *testing.T) {
	when := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	h := &fakeHistory{
		dels: []Deletion{{Path: "cli/projects/-Users-j-Git-api/sess-1.jsonl", Commit: "deadbeef"}},
		// Content is read from the DELETING COMMIT'S PARENT — the last tree that
		// still had the file. Asking for the deleting commit itself would find
		// nothing, which is the mistake this fixture pins.
		blobs: map[string]string{"deadbeef^:cli/projects/-Users-j-Git-api/sess-1.jsonl": "the auth refactor\n/Users/j/Git/api\n"},
		when:  when,
	}
	l, _ := Open(t.TempDir(), "mbp")

	res, err := Backfill(context.Background(), l, h, parseHead)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 || res.Recovered != 1 || res.Skipped != 0 || res.Unreadable != 0 {
		t.Fatalf("result = %+v", res)
	}
	row := l.rows["sess-1"]
	if row.Title != "the auth refactor" || row.Cwd != "/Users/j/Git/api" {
		t.Errorf("row = %+v", row)
	}
	if !row.End.Equal(when) {
		t.Errorf("End = %v, want the last commit that touched the file", row.End)
	}
	if row.Slug != "-Users-j-Git-api" {
		t.Errorf("slug = %q", row.Slug)
	}
}

// A row already in the ledger is left alone: a live transcript is a better source
// than a deleted blob, and re-running the backfill must be a no-op.
func TestBackfill_DoesNotOverwriteKnownRows(t *testing.T) {
	l, _ := Open(t.TempDir(), "mbp")
	live := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	l.Note(Entry{ID: "sess-1", Title: "from the live transcript", End: live, Bytes: 500, Seen: live})

	h := &fakeHistory{
		dels:  []Deletion{{Path: "cli/projects/-slug/sess-1.jsonl", Commit: "c1"}},
		blobs: map[string]string{"c1^:cli/projects/-slug/sess-1.jsonl": "from a deleted blob\n/tmp\n"},
		when:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	res, err := Backfill(context.Background(), l, h, parseHead)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Recovered != 0 {
		t.Fatalf("result = %+v", res)
	}
	if l.rows["sess-1"].Title != "from the live transcript" {
		t.Errorf("known row was overwritten: %+v", l.rows["sess-1"])
	}
	if len(h.asked) != 0 {
		t.Errorf("a known row should not cost a blob read, asked=%v", h.asked)
	}
}

// Subagent transcripts resolve to their PARENT's session id, so recovering them
// would title a session with a subagent's opening line. Non-cli paths aren't
// sessions at all.
func TestBackfill_IgnoresSubagentsAndNonTranscripts(t *testing.T) {
	h := &fakeHistory{
		dels: []Deletion{
			{Path: "cli/projects/-slug/sess-1/subagents/agent-a.jsonl", Commit: "c1"},
			{Path: "desktop/claude-code-sessions/o/u/local_x.json", Commit: "c1"},
			{Path: "cli/projects/-slug/notes.md", Commit: "c1"},
		},
		blobs: map[string]string{},
	}
	l, _ := Open(t.TempDir(), "mbp")
	res, err := Backfill(context.Background(), l, h, parseHead)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 || res.Recovered != 0 {
		t.Fatalf("nothing here is a session transcript: %+v", res)
	}
	if l.Count() != 0 {
		t.Errorf("ledger should be empty, got %d rows", l.Count())
	}
}

// An unreadable blob costs its own row and is reported, not swallowed and not
// fatal — history can be shallow-cloned or the parent commit missing.
func TestBackfill_CountsUnreadable(t *testing.T) {
	h := &fakeHistory{
		dels:  []Deletion{{Path: "cli/projects/-slug/sess-1.jsonl", Commit: "c1"}},
		blobs: map[string]string{}, // ShowPrefix will error
	}
	l, _ := Open(t.TempDir(), "mbp")
	res, err := Backfill(context.Background(), l, h, parseHead)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unreadable != 1 || res.Recovered != 0 || res.Deleted != 1 {
		t.Fatalf("result = %+v", res)
	}
}

// Rows are unioned across every device's file, so a session ANOTHER machine
// already recovered is not new here. Treating it as new would re-read the blob
// and write a duplicate row that then wins on recency while saying nothing the
// other row didn't.
func TestBackfill_SkipsSessionsAnotherDeviceAlreadyRecovered(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	other, _ := Open(dir, "the-other-mac")
	other.Note(Entry{ID: "sess-1", Title: "recovered over there", End: when, Seen: when})
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}

	h := &fakeHistory{
		dels:  []Deletion{{Path: "cli/projects/-slug/sess-1.jsonl", Commit: "c1"}},
		blobs: map[string]string{"c1^:cli/projects/-slug/sess-1.jsonl": "from a deleted blob\n/tmp\n"},
		when:  when,
	}
	l, _ := Open(dir, "this-mac")
	res, err := Backfill(context.Background(), l, h, parseHead)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Recovered != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(h.asked) != 0 {
		t.Errorf("a row another device holds should cost no blob read, asked=%v", h.asked)
	}
	if got := LoadAll(dir)["sess-1"].Title; got != "recovered over there" {
		t.Errorf("the existing row should stand, got %q", got)
	}
}
