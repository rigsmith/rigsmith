package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// FetchMerge fetches remote/branch and merges it into the current branch (a real
// merge, not ff-only — used to reconcile when a push is rejected because the
// remote advanced). It returns conflicted=true (leaving the repo in the merge
// state for the caller to resolve) when the merge hit conflicts, or a non-nil
// error for any other failure.
func (r *Repo) FetchMerge(ctx context.Context, remote, branch string) (conflicted bool, err error) {
	if _, err := runGit(ctx, r.Dir, "fetch", remote, branch); err != nil {
		return false, err
	}
	if _, err := runGit(ctx, r.Dir, "merge", "--no-edit", "FETCH_HEAD"); err != nil {
		if unmerged, _ := runGit(ctx, r.Dir, "ls-files", "-u"); strings.TrimSpace(unmerged) != "" {
			return true, nil // genuine conflicts — repo is mid-merge
		}
		return false, err
	}
	return false, nil
}

// RunMergeTool launches the user's configured `git mergetool` interactively
// (inheriting the terminal). clauderig deliberately does not build a diff/merge
// UI — it hands off to whatever the user already uses.
func (r *Repo) RunMergeTool(ctx context.Context) error {
	return runGitInteractive(ctx, r.Dir, "mergetool")
}

// CommitMerge finishes an in-progress merge after conflicts are resolved.
func (r *Repo) CommitMerge(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "commit", "--no-edit")
	return err
}

// AbortMerge backs out an in-progress merge, restoring the pre-merge state.
func (r *Repo) AbortMerge(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "merge", "--abort")
	return err
}

// runGitInteractive runs git attached to the real terminal (for mergetool).
func runGitInteractive(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
