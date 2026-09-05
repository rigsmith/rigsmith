package gitrepo

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// Squash collapses the current branch to a single root (parent-less) commit whose
// tree is the current HEAD tree, then garbage-collects the now-unreferenced
// history. The working tree is unchanged (same tree). This is how clauderig keeps
// the staging repo bounded: the churny, append-only transcript history would
// otherwise grow .git without limit. Push with ForcePush afterward — the branch's
// history was rewritten.
func (r *Repo) Squash(ctx context.Context, msg string) error {
	tree, err := runGit(ctx, r.Dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	commit, err := runGit(ctx, r.Dir, "commit-tree", strings.TrimSpace(tree), "-m", msg)
	if err != nil {
		return err
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if _, err := runGit(ctx, r.Dir, "update-ref", "refs/heads/"+branch, strings.TrimSpace(commit)); err != nil {
		return err
	}
	// Reclaim the space the squash freed (the point of squashing).
	_, _ = runGit(ctx, r.Dir, "reflog", "expire", "--expire=now", "--all")
	_, _ = runGit(ctx, r.Dir, "gc", "--prune=now", "--quiet")
	return nil
}

// ForcePush force-updates remote's branch to the current HEAD (after a Squash).
func (r *Repo) ForcePush(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "push", "--force", remote, "HEAD:"+branch)
	return err
}

// ForcePushWithLease replaces the remote branch only if it is still at expect.
// A plain force-push deletes whatever another machine pushed in the meantime;
// the lease turns that silent loss into a rejected push.
//
// expect is a sha the caller has actually seen — read it from the remote, not
// from a remote-tracking ref that may be older than the last fetch.
func (r *Repo) ForcePushWithLease(ctx context.Context, remote, branch, expect string) error {
	lease := "--force-with-lease=" + branch
	if expect != "" {
		lease += ":" + expect
	}
	_, err := runGit(ctx, r.Dir, "push", lease, remote, "HEAD:"+branch)
	return err
}

// WorkTreeBytes is the on-disk size of the working tree, excluding .git — the
// "retained working-tree size" the squash threshold is measured against.
func (r *Repo) WorkTreeBytes(ctx context.Context) (int64, error) {
	var total int64
	err := filepath.WalkDir(r.Dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ShouldSquash reports whether .git has grown enough to warrant a squash: it
// exceeds factor × the working-tree size AND is above floorBytes (so small repos
// are never churned). gitBytes/wtBytes come from GitDirBytes/WorkTreeBytes.
func ShouldSquash(gitBytes, wtBytes, floorBytes int64, factor float64) bool {
	if gitBytes <= floorBytes {
		return false
	}
	return float64(gitBytes) > factor*float64(wtBytes)
}
