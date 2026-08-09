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

// Fetch updates remote-tracking refs without touching the working tree, so a
// caller can measure divergence before deciding what to do about it.
func (r *Repo) Fetch(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "fetch", remote, branch)
	return err
}

// MergeRef merges ref into the current branch with an explicit merge commit.
// It returns conflicted=true (leaving the repo mid-merge for the caller to
// resolve) when the merge hit conflicts.
//
// Deliberately a merge and never a rebase: a rebase replays every sync commit
// and re-conflicts on the same files each time, so a 15-commit divergence means
// resolving the same MEMORY.md fifteen times. One merge resolves each file once.
func (r *Repo) MergeRef(ctx context.Context, ref string) (conflicted bool, err error) {
	if _, err := runGit(ctx, r.Dir, "merge", "--no-ff", "--no-edit", ref); err != nil {
		if unmerged, _ := r.UnmergedFiles(ctx); len(unmerged) > 0 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// UnmergedFiles lists the paths left conflicted by an in-progress merge, as
// slash-relative repo paths.
func (r *Repo) UnmergedFiles(ctx context.Context) ([]string, error) {
	out, err := runGit(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// ConflictStages returns the three versions git recorded for a conflicted path:
// the common ancestor, ours, and theirs. A stage that doesn't exist (the file
// was added on one side, or deleted on the other) comes back nil rather than as
// an error — absence is a meaningful state to a resolver, not a failure.
func (r *Repo) ConflictStages(ctx context.Context, path string) (base, ours, theirs []byte, err error) {
	// ls-files -u lists one line per stage: "<mode> <sha> <stage>\t<path>".
	out, err := runGit(ctx, r.Dir, "ls-files", "-u", "-z", "--", path)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, rec := range strings.Split(out, "\x00") {
		meta, _, found := strings.Cut(rec, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		sha, stage := fields[1], fields[2]
		blob, err := runGit(ctx, r.Dir, "cat-file", "blob", sha)
		if err != nil {
			return nil, nil, nil, err
		}
		switch stage {
		case "1":
			base = []byte(blob)
		case "2":
			ours = []byte(blob)
		case "3":
			theirs = []byte(blob)
		}
	}
	return base, ours, theirs, nil
}

// AddPaths stages the given paths, marking a conflicted file resolved.
func (r *Repo) AddPaths(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := runGit(ctx, r.Dir, append([]string{"add", "--"}, paths...)...)
	return err
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
