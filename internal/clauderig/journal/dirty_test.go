package journal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// A restore journals a record but commits nothing, so the line sits pending
// until the next sync sweeps it up. That must not read as "a sync started and
// didn't finish" — otherwise the tray goes amber after every restore, which is
// the same species of misleading indicator the journal exists to remove.
//
// This is the invariant behind status.Gather's DirtyExcluding(journal.DirName);
// it lives here because DirName is the thing that has to stay in step.
func TestPendingRecordIsNotUnfinishedWork(t *testing.T) {
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

	// What a restore does: append, commit nothing.
	if err := Append(dir, Record{Machine: "air", Op: OpRestore, Outcome: OutcomeOK, Files: 900}); err != nil {
		t.Fatal(err)
	}

	dirty, err := repo.DirtyExcluding(ctx, DirName)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Error("a pending journal line read as uncommitted changes — the tray would " +
			"sit amber after every restore")
	}

	// Real unfinished work still registers, so the exclusion isn't a blindfold.
	if err := os.WriteFile(filepath.Join(dir, "synced.txt"), []byte("half-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = repo.DirtyExcluding(ctx, DirName)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("an actual uncommitted change was missed")
	}
}
