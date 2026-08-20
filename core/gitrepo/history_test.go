package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// historyRepo builds a repo whose history contains deleted files, which is the
// only state the backfill reads: a deleted file is gone from the TREE, not from
// history.
func historyRepo(t *testing.T) (context.Context, *Repo, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "cli/projects/-p/keep.jsonl", "kept\n")
	write(t, dir, "cli/projects/-p/gone.jsonl", "first line\nsecond line\n")
	write(t, dir, "cli/projects/-p/twice.jsonl", "v1\n")
	if _, err := r.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	// A file deleted, re-added and deleted again: only its LATEST removal is the
	// copy worth recovering.
	must(t, os.Remove(filepath.Join(dir, "cli/projects/-p/twice.jsonl")))
	if _, err := r.Commit(ctx, "prune twice #1"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "cli/projects/-p/twice.jsonl", "v2\n")
	if _, err := r.Commit(ctx, "re-add twice"); err != nil {
		t.Fatal(err)
	}
	must(t, os.Remove(filepath.Join(dir, "cli/projects/-p/twice.jsonl")))
	must(t, os.Remove(filepath.Join(dir, "cli/projects/-p/gone.jsonl")))
	if _, err := r.Commit(ctx, "retention"); err != nil {
		t.Fatal(err)
	}
	return ctx, r, dir
}

func TestDeletions_FindsRemovedPathsOncePerPath(t *testing.T) {
	ctx, r, _ := historyRepo(t)

	dels, err := r.Deletions(ctx, "cli/projects")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for _, d := range dels {
		byPath[d.Path]++
	}
	if byPath["cli/projects/-p/gone.jsonl"] != 1 {
		t.Errorf("deleted file not reported exactly once: %v", byPath)
	}
	// Deleted twice in history, reported once — and the surviving file never.
	if byPath["cli/projects/-p/twice.jsonl"] != 1 {
		t.Errorf("a path deleted twice should report its latest removal only: %v", byPath)
	}
	if byPath["cli/projects/-p/keep.jsonl"] != 0 {
		t.Errorf("a file still in the tree is not a deletion: %v", byPath)
	}

	// The content lives at the deleting commit's PARENT, and for the re-added file
	// that must be the SECOND version — proving newest-deletion-wins is not just a
	// count.
	for _, d := range dels {
		if !strings.HasSuffix(d.Path, "twice.jsonl") {
			continue
		}
		b, err := r.ShowPrefix(ctx, d.Commit+"^", d.Path, 1<<10)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(b)) != "v2" {
			t.Errorf("recovered the wrong version: %q", b)
		}
	}
}

func TestShowPrefix_CapsBytesAndReportsMissingBlobs(t *testing.T) {
	ctx, r, _ := historyRepo(t)
	dels, err := r.Deletions(ctx, "cli/projects")
	if err != nil {
		t.Fatal(err)
	}
	var gone Deletion
	for _, d := range dels {
		if strings.HasSuffix(d.Path, "gone.jsonl") {
			gone = d
		}
	}
	if gone.Commit == "" {
		t.Fatal("no deletion found for gone.jsonl")
	}

	// The cap is the point: transcripts run to tens of megabytes and only the head
	// is wanted.
	b, err := r.ShowPrefix(ctx, gone.Commit+"^", gone.Path, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 5 || string(b) != "first" {
		t.Errorf("prefix = %q (%d bytes), want the first 5", b, len(b))
	}
	// A short file reads whole rather than erroring on the unmet cap.
	b, err = r.ShowPrefix(ctx, gone.Commit+"^", gone.Path, 1<<20)
	if err != nil || !strings.Contains(string(b), "second line") {
		t.Errorf("whole-file read failed: %q %v", b, err)
	}
	// A path that never existed at that rev is an error, not empty content.
	if _, err := r.ShowPrefix(ctx, gone.Commit+"^", "cli/projects/-p/never.jsonl", 64); err == nil {
		t.Error("a missing blob should report an error")
	}
}

func TestLastCommitTime_IsWhenThePathLastChanged(t *testing.T) {
	ctx, r, dir := historyRepo(t)
	dels, err := r.Deletions(ctx, "cli/projects")
	if err != nil {
		t.Fatal(err)
	}
	var gone Deletion
	for _, d := range dels {
		if strings.HasSuffix(d.Path, "gone.jsonl") {
			gone = d
		}
	}
	got, err := r.LastCommitTime(ctx, gone.Commit+"^", gone.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsZero() {
		t.Fatal("no time for a path that exists at that rev")
	}
	// It must be the commit that last TOUCHED the path, not the tip: later commits
	// exist on this branch that never mention it.
	out, err := runGit(ctx, dir, "log", "-1", "--format=%ct", gone.Commit+"^", "--", gone.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("fixture produced no commit for the path")
	}

	// A path the rev never knew reports ok=false rather than a zero time that
	// reads as a real date.
	if _, err := r.LastCommitTime(ctx, gone.Commit+"^", "cli/projects/-p/never.jsonl"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm, _ := r.LastCommitTime(ctx, gone.Commit+"^", "cli/projects/-p/never.jsonl"); !tm.IsZero() {
		t.Errorf("unknown path should report the zero time, got %v", tm)
	}
}

// git quotes paths with non-ASCII characters unless asked for NUL-delimited
// output, and a quoted display string is not a path ShowPrefix can open — so a
// session in an accented project directory would be reported as unreadable
// rather than recovered. Project slugs come from directory names, so this is
// ordinary, not exotic.
func TestDeletions_HandlesNonASCIIPaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	const rel = "cli/projects/-Users-j-Café/s.jsonl"
	write(t, dir, rel, "first line\nsecond\n")
	if _, err := r.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	must(t, os.Remove(filepath.Join(dir, filepath.FromSlash(rel))))
	if _, err := r.Commit(ctx, "retention"); err != nil {
		t.Fatal(err)
	}

	dels, err := r.Deletions(ctx, "cli/projects")
	if err != nil {
		t.Fatal(err)
	}
	if len(dels) != 1 {
		t.Fatalf("want one deletion, got %+v", dels)
	}
	if dels[0].Path != rel {
		t.Fatalf("path = %q, want the real path (unquoted)", dels[0].Path)
	}
	// And the path it reports must actually resolve to the blob.
	b, err := r.ShowPrefix(ctx, dels[0].Commit+"^", dels[0].Path, 5)
	if err != nil || string(b) != "first" {
		t.Errorf("recovered content = %q, err = %v", b, err)
	}
}
