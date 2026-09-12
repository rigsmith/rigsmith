package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func entry(id string, end time.Time) Entry {
	return Entry{ID: id, End: end, Bytes: 100, Cwd: "/repo", Title: "a conversation", Shard: "sessions/2026/09/05"}
}

func TestARowSurvivesTheRolloutItDescribes(t *testing.T) {
	// The whole point: the tree is a rolling window, this is not.
	dir := t.TempDir()
	l, err := Open(dir, "one")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Truncate(time.Second)
	if !l.Note(entry("s1", end)) {
		t.Fatal("a new session should be recorded")
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	all := LoadAll(dir)
	got, ok := all["s1"]
	if !ok {
		t.Fatal("the session was not remembered")
	}
	if got.Title != "a conversation" || got.Cwd != "/repo" || got.Shard != "sessions/2026/09/05" {
		t.Errorf("row = %+v, want enough to find it again", got)
	}
	if got.RecordedBy != "one" {
		t.Errorf("RecordedBy = %q, want the machine whose sync wrote it", got.RecordedBy)
	}
}

func TestFreshSkipsARolloutThatCannotHaveChanged(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, "one")
	end := time.Now().UTC().Truncate(time.Second)
	l.Note(entry("s1", end))

	if !l.Fresh("s1", end, 100) {
		t.Error("a session at the same size and end time is fresh; re-reading it puts a tail read per conversation into every sync")
	}
	if l.Fresh("s1", end.Add(time.Minute), 100) {
		t.Error("a later end time means the conversation moved")
	}
	if l.Fresh("s1", end, 200) {
		t.Error("a different size means the file grew")
	}
	if l.Fresh("unknown", end, 100) {
		t.Error("a session never seen is not fresh")
	}
}

func TestAnEmptyFieldNeverBlanksOutWhatIsKnown(t *testing.T) {
	// A sync whose tail read failed, or that caught a rollout mid-write, must
	// not erase a title somebody could otherwise have searched for.
	dir := t.TempDir()
	l, _ := Open(dir, "one")
	end := time.Now().UTC().Truncate(time.Second)
	l.Note(entry("s1", end))
	l.Note(Entry{ID: "s1", End: end.Add(time.Minute), Bytes: 250}) // no title, no cwd

	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	got := LoadAll(dir)["s1"]
	if got.Title != "a conversation" || got.Cwd != "/repo" {
		t.Errorf("row = %+v, want the known fields carried forward", got)
	}
	if got.Bytes != 250 || !got.End.Equal(end.Add(time.Minute)) {
		t.Errorf("row = %+v, want the fields that DID arrive updated", got)
	}
}

func TestAnUnchangedRowIsNotRewritten(t *testing.T) {
	// Seen moves on every sync; if it counted as a change, every run would
	// rewrite the whole file and every sync would show a diff.
	dir := t.TempDir()
	l, _ := Open(dir, "one")
	end := time.Now().UTC().Truncate(time.Second)
	l.Note(entry("s1", end))
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	l2, _ := Open(dir, "one")
	if l2.Note(entry("s1", end)) {
		t.Error("re-recording an identical session reported a change")
	}
}

func TestTwoMachinesRememberOneSessionAndTheFullerCopyWins(t *testing.T) {
	dir := t.TempDir()
	end := time.Now().UTC().Truncate(time.Second)

	one, _ := Open(dir, "one")
	one.Note(Entry{ID: "s1", End: end, Bytes: 100, Title: "early"})
	if err := one.Save(); err != nil {
		t.Fatal(err)
	}
	// The second machine saw more of the same conversation.
	two, _ := Open(dir, "two")
	two.Note(Entry{ID: "s1", End: end.Add(time.Hour), Bytes: 900, Title: "later"})
	if err := two.Save(); err != nil {
		t.Fatal(err)
	}

	// One file each: two machines appending to one file conflict on every sync.
	for _, name := range []string{"one.jsonl", "two.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, DirName, name)); err != nil {
			t.Errorf("expected a file per device: %v", err)
		}
	}
	got := LoadAll(dir)["s1"]
	if got.Title != "later" {
		t.Errorf("row = %+v, want the machine that saw more of the conversation", got)
	}
}

func TestFieldsFromANewerBinaryAreNotStrippedOut(t *testing.T) {
	// An older codexrig syncing the same repo must not converge the fleet on
	// whatever the oldest machine happens to understand.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"id":"s1","end":"2026-09-05T00:00:00Z","seen":"2026-09-05T00:00:00Z","title":"kept","somethingNew":{"a":1}}` + "\n"
	p := filepath.Join(dir, DirName, "one.jsonl")
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Open(dir, "one")
	if err != nil {
		t.Fatal(err)
	}
	l.Note(Entry{ID: "s1", End: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), Bytes: 50})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "somethingNew") {
		t.Errorf("a field this binary does not understand was stripped:\n%s", b)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &m); err != nil {
		t.Fatal(err)
	}
	if m["title"] != "kept" {
		t.Errorf("the known fields were damaged: %v", m)
	}
}

func TestATruncatedLineDoesNotLoseTheRest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"s1","end":"2026-09-05T00:00:00Z","seen":"2026-09-05T00:00:00Z"}` + "\n" +
		`{"id":"s2","end":"2026-09-0` + "\n" +
		`{"id":"s3","end":"2026-09-07T00:00:00Z","seen":"2026-09-07T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, DirName, "one.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	all := LoadAll(dir)
	if len(all) != 2 || all["s1"].ID == "" || all["s3"].ID == "" {
		t.Errorf("got %d rows, want the two readable ones", len(all))
	}
}

func TestADeviceNameIsSanitisedIntoAFilename(t *testing.T) {
	// A device name comes from a hostname, and a hostname is not a filename.
	cases := map[string]string{
		"Johns-MacBook-Pro.local": "Johns-MacBook-Pro.local.jsonl",
		"work/laptop":             "work-laptop.jsonl",
		"":                        "unknown.jsonl",
		"...":                     "unknown.jsonl",
	}
	for in, want := range cases {
		if got := fileName(in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}
