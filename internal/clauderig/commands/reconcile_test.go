package commands

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wedged reproduces the state that blocks every later sync: a staging repo left
// sitting in an unfinished merge.
func wedged(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@example.com")
	mustGit(t, dir, "config", "user.name", "t")
	put(t, dir, "cli/projects/p/memory/note.md", "# note\nshared\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "base")

	mustGit(t, dir, "checkout", "-b", "other")
	put(t, dir, "cli/projects/p/memory/note.md", "# note\nshared\nfrom the desktop\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "other machine")

	mustGit(t, dir, "checkout", "main")
	put(t, dir, "cli/projects/p/memory/note.md", "# note\nshared\nfrom the laptop\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "this machine")

	_ = exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run() // conflicts on purpose
	return dir
}

// The repair has to work with no terminal attached — the SessionStart hook and any
// agent-driven sync run that way, and that is precisely when the repo gets wedged.
func TestRepairWedgedMerge_SettlesWithoutATerminal(t *testing.T) {
	dir := wedged(t)
	ctx := context.Background()
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.InMerge(ctx) {
		t.Fatal("setup did not leave the repo mid-merge")
	}

	var out bytes.Buffer
	if safe := repairWedgedMerge(ctx, &out, dir, false); !safe {
		t.Fatalf("a settleable merge should report the repo safe to write; output:\n%s", out.String())
	}

	if repo.InMerge(ctx) {
		t.Fatalf("still mid-merge after repair; output:\n%s", out.String())
	}
	left, err := repo.Conflicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("conflicts remain: %v", left)
	}
	b, err := os.ReadFile(filepath.Join(dir, "cli/projects/p/memory/note.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "<<<<<<<") {
		t.Fatalf("conflict markers were committed into the file:\n%s", got)
	}
	for _, want := range []string{"from the laptop", "from the desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("repair lost %q:\n%s", want, got)
		}
	}
}

// A clean repo must be left completely alone — the repair runs on every session
// start, so it has to be a no-op in the normal case.
func TestRepairWedgedMerge_NoOpOnCleanRepo(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@example.com")
	mustGit(t, dir, "config", "user.name", "t")
	put(t, dir, "a.md", "hello\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "base")

	ctx := context.Background()
	repo, _ := gitrepo.Open(ctx, dir)
	before, _ := repo.Head(ctx)

	var out bytes.Buffer
	repairWedgedMerge(ctx, &out, dir, false)

	after, _ := repo.Head(ctx)
	if before != after {
		t.Errorf("repair moved HEAD on a clean repo: %s -> %s", before, after)
	}
	if out.Len() != 0 {
		t.Errorf("repair spoke on a clean repo: %q", out.String())
	}
}

// The safe/unsafe answer is what sync stakes its abort on: `git add -A` over a
// still-conflicted index marks the conflicts resolved with their markers intact,
// so a caller that stages and commits must stop. The contract is therefore "is
// the repo out of the merge", never "did the repair try" — and a repair can fail
// WITHOUT aborting, which is the case that matters. Forced here with a
// pre-commit hook that refuses, so the policies resolve every conflict and the
// merge commit is what fails.
func TestRepairWedgedMerge_UnsafeWhenTheMergeCannotBeCommitted(t *testing.T) {
	dir := wedged(t)
	ctx := context.Background()
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "config", "core.hooksPath", hooks)

	var out bytes.Buffer
	safe := repairWedgedMerge(ctx, &out, dir, false)

	if !repo.InMerge(ctx) {
		t.Skip("this git finished the merge anyway; the invariant below has nothing to pin")
	}
	if safe {
		t.Fatalf("reported safe while still mid-merge — the exact state that publishes conflict markers; output:\n%s", out.String())
	}
}
