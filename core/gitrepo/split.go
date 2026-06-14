package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CommitSubtree commits the working-tree files matching pathspecs to branch using
// a temporary index, so it doesn't disturb the main branch's index/HEAD. It is
// how clauderig keeps a config-history branch (everything except cli/projects)
// alongside main: main is squashed to stay bounded, while config-history accretes
// small config-only commits that survive the squash. Returns changed=false when
// the resulting tree equals the branch's current tree.
//
// pathspecs use git's pathspec syntax, e.g. {".", ":!cli/projects"} for
// "everything except the transcript tree".
func (r *Repo) CommitSubtree(ctx context.Context, branch string, pathspecs []string, msg string) (bool, error) {
	gd, err := r.gitDir(ctx)
	if err != nil {
		return false, err
	}
	idx := filepath.Join(gd, "clauderig-idx-"+branch)
	_ = os.Remove(idx)
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}

	add := append([]string{"add", "-A", "--"}, pathspecs...)
	if _, err := runGitEnv(ctx, r.Dir, env, add...); err != nil {
		return false, err
	}
	treeOut, err := runGitEnv(ctx, r.Dir, env, "write-tree")
	if err != nil {
		return false, err
	}
	tree := strings.TrimSpace(treeOut)

	var parent string
	if prev, err := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil && strings.TrimSpace(prev) != "" {
		parent = strings.TrimSpace(prev)
		if pt, err := runGit(ctx, r.Dir, "rev-parse", branch+"^{tree}"); err == nil && strings.TrimSpace(pt) == tree {
			return false, nil // unchanged
		}
	}

	commitArgs := []string{"commit-tree", tree, "-m", msg}
	if parent != "" {
		commitArgs = []string{"commit-tree", tree, "-p", parent, "-m", msg}
	}
	commit, err := runGit(ctx, r.Dir, commitArgs...)
	if err != nil {
		return false, err
	}
	if _, err := runGit(ctx, r.Dir, "update-ref", "refs/heads/"+branch, strings.TrimSpace(commit)); err != nil {
		return false, err
	}
	return true, nil
}

// PushBranch pushes a local branch ref to the same-named branch on remote.
func (r *Repo) PushBranch(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "push", remote, branch+":"+branch)
	return err
}

// ForcePushBranch force-pushes a local branch (after SquashBranch rewrote it).
func (r *Repo) ForcePushBranch(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "push", "--force", remote, branch+":"+branch)
	return err
}

// BranchCommitCount returns how many commits branch has, or 0 if it doesn't exist.
func (r *Repo) BranchCommitCount(ctx context.Context, branch string) int {
	out, err := runGit(ctx, r.Dir, "rev-list", "--count", branch)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range strings.TrimSpace(out) {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// SquashBranch collapses a branch (not necessarily the current one) to a single
// parent-less commit holding its current tree — bounding a side branch like
// config-history whose commit count would otherwise grow without limit.
func (r *Repo) SquashBranch(ctx context.Context, branch, msg string) error {
	tree, err := runGit(ctx, r.Dir, "rev-parse", branch+"^{tree}")
	if err != nil {
		return err
	}
	commit, err := runGit(ctx, r.Dir, "commit-tree", strings.TrimSpace(tree), "-m", msg)
	if err != nil {
		return err
	}
	_, err = runGit(ctx, r.Dir, "update-ref", "refs/heads/"+branch, strings.TrimSpace(commit))
	return err
}

func (r *Repo) gitDir(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.Dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir, p)
	}
	return p, nil
}

func runGitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
