package gitrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	Path    string
	Branch  string // short name, or "" when detached
	Head    string
	ModTime time.Time // last time git touched this worktree (commit/checkout), or its creation; zero if unknown
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
	for i := range list {
		list[i].ModTime = worktreeModTime(list[i].Path)
	}
	return list, nil
}

// worktreeModTime reports when git last touched the worktree at path — a stand-in
// for "created or last updated". It stats the worktree's private git admin
// directory, where git writes HEAD/index/logs: that directory's mtime is set when
// `git worktree add` creates it and advances on every commit, checkout, or reset
// (each rewrites a file there via rename, which bumps the directory mtime). The
// admin dir is resolved from the worktree's `.git` pointer — a file holding
// `gitdir: <admindir>` for a linked worktree, or the `.git` directory itself for
// the main checkout. A zero time is returned when nothing can be stat'd, so
// callers sort such worktrees last rather than crashing.
func worktreeModTime(path string) time.Time {
	dotgit := filepath.Join(path, ".git")
	fi, err := os.Stat(dotgit)
	if err != nil {
		return time.Time{}
	}
	admin := dotgit
	if !fi.IsDir() {
		if data, err := os.ReadFile(dotgit); err == nil {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:"); ok {
				admin = strings.TrimSpace(rest)
				// The pointer is relative to the worktree dir when git wrote it in
				// relative-paths mode (git 2.48+ --relative-paths /
				// worktree.useRelativePaths); resolve it so the stat doesn't miss.
				if !filepath.IsAbs(admin) {
					admin = filepath.Join(path, admin)
				}
			}
		}
	}
	if ai, err := os.Stat(admin); err == nil {
		return ai.ModTime()
	}
	return fi.ModTime()
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

// WorktreePruneMeta runs `git worktree prune`, clearing the administrative
// records of worktrees whose directories were deleted by hand. It does not
// touch any existing checkout.
func (r *Repo) WorktreePruneMeta(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "worktree", "prune")
	return err
}

// WorktreeClean reports whether the worktree rooted at path has no uncommitted
// or untracked changes. path is the worktree's own directory (git is run there
// so the answer is that tree's status, not the main checkout's).
func (r *Repo) WorktreeClean(ctx context.Context, path string) (bool, error) {
	out, err := runGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// IsMerged reports whether branch's changes are already contained in base. It
// recognises two shapes of "merged":
//
//   - ordinary merge / fast-forward — branch's tip is an ancestor of base; and
//   - squash-merge — base holds an equivalent patch under a different commit
//     (so branch's tip is NOT an ancestor). This is the common case here, since
//     PRs land as a single squashed commit, and a plain ancestor test would
//     wrongly call every merged branch un-merged.
//
// The squash case is detected by synthesising a commit that carries branch's
// tree on top of the merge-base, then asking `git cherry` whether base already
// contains an equivalent change (matched by patch-id). It compares against the
// *local* base ref, so keep base current (e.g. pull main) for accurate results;
// when in doubt it errs toward "not merged", which only means a worktree is
// kept rather than wrongly removed.
func (r *Repo) IsMerged(ctx context.Context, branch, base string) (bool, error) {
	code, err := gitExitCode(ctx, r.Dir, "merge-base", "--is-ancestor", branch, base)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil // ancestor → ordinary merge or fast-forward
	case 1:
		// Not an ancestor — fall through to the squash-merge check.
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: exit %d", branch, base, code)
	}

	mergeBase, err := runGit(ctx, r.Dir, "merge-base", base, branch)
	if err != nil {
		return false, err
	}
	tree, err := runGit(ctx, r.Dir, "rev-parse", branch+"^{tree}")
	if err != nil {
		return false, err
	}
	synthetic, err := runGit(ctx, r.Dir, "commit-tree", strings.TrimSpace(tree),
		"-p", strings.TrimSpace(mergeBase), "-m", "rigsmith prune merge-check")
	if err != nil {
		return false, err
	}
	// git cherry prefixes a commit with '-' when base already has an equivalent
	// patch, '+' when it does not. Our synthetic commit is the only one ahead of
	// the merge-base, so a leading '-' means base contains branch's changes.
	cherry, err := runGit(ctx, r.Dir, "cherry", base, strings.TrimSpace(synthetic))
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.TrimSpace(cherry), "-"), nil
}

// DeleteBranch deletes a local branch. Without force it uses `branch -d`, which
// refuses to drop a branch git still considers unmerged.
func (r *Repo) DeleteBranch(ctx context.Context, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := runGit(ctx, r.Dir, "branch", flag, branch)
	return err
}

// Branch is one local branch from LocalBranches.
type Branch struct {
	Name    string
	Current bool // the checked-out branch in this worktree
	Gone    bool // had an upstream that no longer exists on the remote
}

// LocalBranches lists the repo's local branches with the upstream-tracking state
// the prune logic needs. A "gone" upstream — git's marker for a remote branch
// that was deleted — is the practical signal that a PR merged and the forge
// auto-removed its branch; it's how a squash-merged branch is recognised once
// the mainline has diverged too far for a patch-id match to hold.
func (r *Repo) LocalBranches(ctx context.Context) ([]Branch, error) {
	// NUL-separate fields so branch names with spaces survive; one branch per line.
	out, err := runGit(ctx, r.Dir, "for-each-ref",
		"--format=%(refname:short)%00%(HEAD)%00%(upstream:track)",
		"refs/heads/")
	if err != nil {
		return nil, err
	}
	var list []Branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) < 3 {
			continue
		}
		list = append(list, Branch{
			Name:    f[0],
			Current: f[1] == "*",
			Gone:    strings.Contains(f[2], "gone"),
		})
	}
	return list, nil
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
