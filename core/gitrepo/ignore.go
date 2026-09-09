package gitrepo

import (
	"context"

	"github.com/rigsmith/rigsmith/core/commandrun"
)

// IsIgnored reports whether path (relative to the work tree) is already covered by
// a gitignore rule — so a caller can skip adding a redundant entry that some
// broader pattern already matches. `git check-ignore -q` exits 0 when ignored.
// This legacy boolean also returns false on command/cleanup failures. A caller
// must not use it to authorize mutations with a selected command runner; that
// requires an error-bearing probe and workflow-level failure propagation.
func (r *Repo) IsIgnored(ctx context.Context, path string) bool {
	cmd := commandrun.Command(ctx, "git", "check-ignore", "-q", path)
	cmd.Dir = r.Dir
	return commandrun.Run(ctx, cmd) == nil
}
