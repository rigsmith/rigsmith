package gitrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// commitAt makes a commit with a fixed committer date, so a test can build a
// history that spans weeks without waiting for any.
func commitAt(t *testing.T, ctx context.Context, r *Repo, name, body string, at time.Time) {
	t.Helper()
	write(t, r.Dir, name, body)
	stamp := at.Format(time.RFC3339)
	if _, err := runGitEnv(ctx, r.Dir, nil, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	env := []string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp}
	if _, err := runGitEnv(ctx, r.Dir, env, "commit", "-m", "sync "+name); err != nil {
		t.Fatal(err)
	}
}

func agedRepo(t *testing.T) (context.Context, *Repo, time.Time) {
	t.Helper()
	ctx := context.Background()
	r, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i, d := range []int{40, 30, 20, 10, 3, 1} {
		commitAt(t, ctx, r, "f.txt", strings.Repeat("v", i+1), now.AddDate(0, 0, -d))
	}
	return ctx, r, now
}

// Folding old history must not change what is checked out — the point is to stop
// paying for the steps that got here, not to lose where "here" is.
func TestSquashBefore_KeepsTheTreeAndTheRecentCommits(t *testing.T) {
	ctx, r, now := agedRepo(t)

	before, err := runGit(ctx, r.Dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}

	folded, err := r.SquashBefore(ctx, now.AddDate(0, 0, -7), "clauderig: history before this point")
	if err != nil {
		t.Fatal(err)
	}
	// 40, 30, 20 and 10 days old are all older than the cutoff; the last of them
	// becomes the base, so three are folded away.
	if folded != 3 {
		t.Errorf("folded = %d, want 3", folded)
	}

	after, err := runGit(ctx, r.Dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Error("the working tree changed — a squash must only drop history")
	}

	// base + the two commits inside the window.
	out, err := runGit(ctx, r.Dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "3" {
		t.Errorf("commits = %s, want 3 (base + 3d + 1d)", got)
	}

	// The kept commits have to be the real ones, with their dates intact.
	log, err := runGit(ctx, r.Dir, "log", "--format=%s %cI", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "history before this point") {
		t.Errorf("no base commit in:\n%s", log)
	}
	if strings.Count(log, "sync f.txt") != 2 {
		t.Errorf("kept commits lost their messages:\n%s", log)
	}
	want := now.AddDate(0, 0, -1).Format("2006-01-02")
	if !strings.Contains(log, want) {
		t.Errorf("kept commits lost their dates, want one on %s:\n%s", want, log)
	}
}

// Nothing old enough is the ordinary case on a young repo, and must be a no-op
// rather than a rewrite that force-pushes for no reason.
func TestSquashBefore_NoOpWhenNothingIsOldEnough(t *testing.T) {
	ctx, r, now := agedRepo(t)
	head, err := runGit(ctx, r.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	folded, err := r.SquashBefore(ctx, now.AddDate(0, 0, -365), "unused")
	if err != nil {
		t.Fatal(err)
	}
	if folded != 0 {
		t.Errorf("folded = %d, want 0", folded)
	}
	now2, err := runGit(ctx, r.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head != now2 {
		t.Error("HEAD moved on a no-op")
	}
}

// A cutoff past everything leaves exactly the base commit.
func TestSquashBefore_CollapsesEverythingWhenCutoffIsNow(t *testing.T) {
	ctx, r, now := agedRepo(t)
	if _, err := r.SquashBefore(ctx, now.Add(time.Hour), "clauderig: squashed"); err != nil {
		t.Fatal(err)
	}
	out, err := runGit(ctx, r.Dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("commits = %s, want 1", got)
	}
}

func TestStats_ReportsShape(t *testing.T) {
	ctx, r, _ := agedRepo(t)
	s, err := r.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Commits != 6 {
		t.Errorf("Commits = %d, want 6", s.Commits)
	}
	if s.Files != 1 {
		t.Errorf("Files = %d, want 1", s.Files)
	}
	if s.GitBytes <= 0 || s.WorkBytes <= 0 {
		t.Errorf("sizes not measured: %+v", s)
	}
	if s.First.IsZero() || s.Last.IsZero() || !s.First.Before(s.Last) {
		t.Errorf("First/Last wrong: %v .. %v", s.First, s.Last)
	}
}
