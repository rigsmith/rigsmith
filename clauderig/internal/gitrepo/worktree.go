package gitrepo

import (
	"context"
	"strings"
)

// Toplevel returns the absolute root of the work tree containing r.Dir.
func (r *Repo) Toplevel(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.Dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// BranchExists reports whether a local branch named name exists.
func (r *Repo) BranchExists(ctx context.Context, name string) bool {
	_, err := runGit(ctx, r.Dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// DefaultBranch picks the repo's mainline: origin's HEAD if known, else the
// first of main/master/trunk that exists, else the current branch.
func (r *Repo) DefaultBranch(ctx context.Context) string {
	if out, err := runGit(ctx, r.Dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "origin/")); b != "" {
			return b
		}
	}
	for _, b := range []string{"main", "master", "trunk"} {
		if r.BranchExists(ctx, b) {
			return b
		}
	}
	if b, err := r.CurrentBranch(ctx); err == nil {
		return b
	}
	return "main"
}

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string // short name, or "" when detached
	Head   string
}

// WorktreeAdd creates a worktree at path. When create is set it makes a new
// branch off base; otherwise it checks out the existing branch.
func (r *Repo) WorktreeAdd(ctx context.Context, path, branch, base string, create bool) error {
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "-b", branch, path, base)
	} else {
		args = append(args, path, branch)
	}
	_, err := runGit(ctx, r.Dir, args...)
	return err
}

// WorktreeList returns the repo's linked worktrees (including the main one).
func (r *Repo) WorktreeList(ctx context.Context) ([]Worktree, error) {
	out, err := runGit(ctx, r.Dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" {
			list = append(list, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return list, nil
}

// WorktreeRemove removes the worktree at path (force drops a dirty tree).
func (r *Repo) WorktreeRemove(ctx context.Context, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := runGit(ctx, r.Dir, args...)
	return err
}

// CommittableFiles lists the repo-relative paths a `git commit` would record:
// staged changes, plus tracked-but-unstaged modifications when withAll is set
// (the `git commit -a` case). The result feeds the guard's base-branch check.
func (r *Repo) CommittableFiles(ctx context.Context, withAll bool) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(out string) {
		for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
			if f != "" && !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	staged, err := runGit(ctx, r.Dir, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	add(staged)
	if withAll {
		tracked, err := runGit(ctx, r.Dir, "diff", "--name-only")
		if err != nil {
			return nil, err
		}
		add(tracked)
	}
	return files, nil
}
