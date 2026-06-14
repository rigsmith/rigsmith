package gitrepo

import (
	"context"
	"fmt"
	"strings"
)

// LastCommit returns the short hash, subject, and relative time of HEAD.
func (r *Repo) LastCommit(ctx context.Context) (hash, subject, when string, err error) {
	out, err := runGit(ctx, r.Dir, "log", "-1", "--format=%h%x1f%s%x1f%cr")
	if err != nil {
		return "", "", "", err
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x1f", 3)
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("gitrepo: unexpected log output %q", out)
	}
	return parts[0], parts[1], parts[2], nil
}

// Reachable reports whether url responds to ls-remote (network check). An empty
// but reachable remote still counts as reachable.
func Reachable(ctx context.Context, url string) bool {
	_, err := runGit(ctx, "", "ls-remote", url)
	return err == nil
}
