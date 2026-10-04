package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteNewChangesetSkipsTakenName pins the clobber bug: the generator
// picked jolly-geckos-wander, which was already a committed, pending
// changeset, and add overwrote it while reporting "Created". A taken name must
// be skipped and the existing file left byte-identical.
func TestWriteNewChangesetSkipsTakenName(t *testing.T) {
	dir := t.TempDir()
	existing := []byte("---\n\"Tweed.App\": minor\n---\n\nWatch a PR's reviews.\n")
	taken := filepath.Join(dir, "jolly-geckos-wander.md")
	if err := os.WriteFile(taken, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	names := []string{"jolly-geckos-wander", "calm-otters-dance"}
	prev := newChangesetID
	newChangesetID = func() string { n := names[0]; names = names[1:]; return n }
	t.Cleanup(func() { newChangesetID = prev })

	id, err := writeNewChangeset(dir, "the new changeset\n")
	if err != nil {
		t.Fatalf("writeNewChangeset: %v", err)
	}
	if id != "calm-otters-dance" {
		t.Fatalf("id = %q, want the next free name %q", id, "calm-otters-dance")
	}
	got, err := os.ReadFile(taken)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("existing changeset was modified:\n got %q\nwant %q", got, existing)
	}
	written, err := os.ReadFile(filepath.Join(dir, id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "the new changeset\n" {
		t.Fatalf("new changeset = %q", written)
	}
}

// TestWriteNewChangesetGivesUpWhenEveryNameIsTaken: a generator that only ever
// offers taken names must end in an error, not an overwrite or a hang.
func TestWriteNewChangesetGivesUpWhenEveryNameIsTaken(t *testing.T) {
	dir := t.TempDir()
	taken := filepath.Join(dir, "jolly-geckos-wander.md")
	if err := os.WriteFile(taken, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := newChangesetID
	newChangesetID = func() string { return "jolly-geckos-wander" }
	t.Cleanup(func() { newChangesetID = prev })

	if _, err := writeNewChangeset(dir, "new"); err == nil {
		t.Fatal("want an error when no free name can be found")
	}
	if got, _ := os.ReadFile(taken); string(got) != "keep" {
		t.Fatalf("existing changeset was modified: %q", got)
	}
}
