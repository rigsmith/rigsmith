package gitrepo

import (
	"context"

	"github.com/rigsmith/rigsmith/core/commandrun"
)

// IsIgnored reports whether path (relative to the work tree) is already covered by
// a gitignore rule — so a caller can skip adding a redundant entry that some
// broader pattern already matches. `git check-ignore -q` exits 0 when ignored.
// This legacy boolean also returns false on command/cleanup failures. Mutating
// selected-runner callers must use CheckIgnored and propagate its error.
func (r *Repo) IsIgnored(ctx context.Context, path string) bool {
	cmd := commandrun.Command(ctx, "git", "check-ignore", "-q", path)
	cmd.Dir = r.Dir
	return commandrun.Run(ctx, cmd) == nil
}

// CheckIgnored distinguishes a non-ignored path (exit 1) from a failed probe.
func (r *Repo) CheckIgnored(ctx context.Context, path string) (bool, error) {
	_, err := runGit(ctx, r.Dir, "check-ignore", "-q", path)
	if err == nil {
		return true, nil
	}
	if exitStatus(err) == 1 {
		return false, nil
	}
	return false, err
}
