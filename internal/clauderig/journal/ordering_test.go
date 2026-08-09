package journal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// These two tests encode the ordering rule in the package doc, because it is
// the one thing about the journal that is easy to get wrong later and
// impossible to notice by reading: the record has to be appended *before* the
// commit that carries it.
//
// Get it backwards and every sync leaves an uncommitted journal line behind,
// `status` reports uncommitted changes forever, and the tray sits amber on a
// perfectly healthy machine — replacing one misleading indicator with another,
// which is exactly what this work set out to stop.

func TestAppendBeforeCommitLeavesTreeClean(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Something for the sync commit to carry, as a real sync would have.
	if err := os.WriteFile(filepath.Join(dir, "synced.txt"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, Record{Machine: "air", Op: OpSync, Outcome: OutcomeOK, Files: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "clauderig sync: air"); err != nil {
		t.Fatal(err)
	}

	dirty, err := repo.Dirty(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("journal appended before the commit still left the tree dirty — " +
			"status would report uncommitted changes after every sync")
	}

	// And the record actually travelled, so other machines will see it.
	recs, err := Read(dir, 0)
	if err != nil || len(recs) != 1 {
		t.Fatalf("record did not survive the commit: %d records, err=%v", len(recs), err)
	}
}

// The mirror image, kept as a test so the reason for the rule is visible rather
// than folded into a comment: appending after the commit is what leaves the
// tree dirty.
func TestAppendAfterCommitLeavesTreeDirty(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "synced.txt"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "clauderig sync: air"); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, Record{Machine: "air", Op: OpSync, Outcome: OutcomeOK}); err != nil {
		t.Fatal(err)
	}

	dirty, err := repo.Dirty(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("expected a post-commit append to leave the tree dirty; if this " +
			"ever stops being true, the ordering rule in the package doc can be relaxed")
	}
}
