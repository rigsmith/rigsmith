package gitrepo

import (
	"context"
	"os/exec"
)

// IsIgnored reports whether path (relative to the work tree) is already covered by
// a gitignore rule — so a caller can skip adding a redundant entry that some
// broader pattern already matches. `git check-ignore -q` exits 0 when ignored.
func (r *Repo) IsIgnored(ctx context.Context, path string) bool {
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "-q", path)
	cmd.Dir = r.Dir
	return cmd.Run() == nil
}
