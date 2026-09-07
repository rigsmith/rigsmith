package commitartifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// SettledHead reads only committed canonical history under the caller's staging
// lease. Missing repositories and confirmed unborn branches return empty. An
// unfinished operation or invalid repository fails closed; no index or worktree
// changes are made. Inherited Git environment cannot redirect the inspection.
func SettledHead(ctx context.Context, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", ErrInvalid
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	repo := gitRepo{dir: dir}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		p, err := repo.run(ctx, nil, "rev-parse", "--git-path", name)
		if err != nil {
			return "", err
		}
		p = strings.TrimSpace(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err = os.Lstat(p); err == nil {
			return "", ErrConflict
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	unmerged, err := repo.run(ctx, nil, "ls-files", "--unmerged")
	if err != nil {
		return "", err
	}
	if unmerged != "" {
		return "", ErrConflict
	}
	head, err := repo.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil {
		head = strings.TrimSpace(head)
		if !objectID(head) {
			return "", ErrInvalid
		}
		return head, nil
	}
	// Only a clean rev-parse failure can enter unborn-branch detection. A
	// cleanup or output failure must not be hidden by later successful probes.
	if !gitExited(err, 128) {
		return "", err
	}
	// Only a symbolic HEAD whose exact branch is absent is an unborn checkout.
	ref, refErr := repo.run(ctx, nil, "symbolic-ref", "--quiet", "HEAD")
	if refErr != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", ErrInvalid
	}
	_, refErr = repo.run(ctx, nil, "show-ref", "--verify", "--quiet", ref)
	if gitExited(refErr, 1) && ctx.Err() == nil {
		return "", nil
	}
	return "", err
}
