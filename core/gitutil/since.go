package gitutil

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ChangedFilesSince returns the absolute paths of every file that differs
// between the merge-base of ref and HEAD and the current working tree (so
// uncommitted changes are included). Ported from net-changesets'
// GitService.GetChangedFilesSinceAsync: paths are diffed with --no-relative and
// resolved against the repository root, so the result is independent of the
// directory the command runs in. An invalid ref (or absent git/repo) is an
// error — the caller surfaces it rather than treating it as "no changes".
func ChangedFilesSince(ctx context.Context, dir, ref string) ([]string, error) {
	root, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("gitutil: not a git repository: %w", err)
	}
	base, err := runGit(ctx, dir, "merge-base", ref, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("gitutil: %q is not a valid ref: %w", ref, err)
	}
	out, err := runGit(ctx, dir, "diff", "--name-only", "--no-relative", strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("gitutil: diff since %s: %w", ref, err)
	}

	repoRoot := strings.TrimSpace(root)
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, filepath.Join(repoRoot, filepath.FromSlash(line)))
	}
	return files, nil
}

// CommitsSince returns the full SHAs of the commits reachable from HEAD but not
// from the merge-base of ref and HEAD: the commits a branch adds since ref. An
// invalid ref (or absent git/repo) is an error, as for ChangedFilesSince.
func CommitsSince(ctx context.Context, dir, ref string) (map[string]bool, error) {
	base, err := runGit(ctx, dir, "merge-base", ref, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("gitutil: %q is not a valid ref: %w", ref, err)
	}
	out, err := runGit(ctx, dir, "rev-list", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("gitutil: commits since %s: %w", ref, err)
	}
	commits := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commits[line] = true
		}
	}
	return commits, nil
}
