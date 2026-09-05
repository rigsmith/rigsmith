package gitrepo

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SquashBefore collapses everything older than cutoff into a single parent-less
// base commit and keeps the commits at or after it, unchanged. Returns how many
// commits were folded away; 0 means there was nothing old enough and the repo
// was left alone.
//
// Squash() is the all-or-nothing version and throws away every intermediate
// state. This exists because "the last week is worth keeping, the year before it
// is not" is the shape the question actually takes — you want to be able to see
// what a sync did recently, without paying for history back to the beginning.
//
// The kept commits are rebuilt with commit-tree rather than replayed with
// rebase. Each keeps its exact tree, message, author and dates; nothing is
// diffed or merged, so there is no conflict to resolve and no chance of a
// replayed commit producing a tree that differs from the one it recorded. It is
// also linear in commits rather than in content, which matters when a week of
// syncing is a couple of thousand of them.
//
// The branch's history is rewritten, so a ForcePush must follow.
func (r *Repo) SquashBefore(ctx context.Context, cutoff time.Time, msg string) (folded int, err error) {
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		return 0, err
	}
	// Oldest-first, so the rebuild below can walk parents forward.
	out, err := runGit(ctx, r.Dir, "rev-list", "--reverse", "--format=%H %cI", "--no-commit-header", "HEAD")
	if err != nil {
		return 0, err
	}
	type commit struct {
		sha string
		at  time.Time
	}
	var all []commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, iso, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		at, perr := time.Parse(time.RFC3339, iso)
		if perr != nil {
			// A commit whose date cannot be read must not be silently folded
			// away — treat it as recent and keep it.
			at = time.Now()
		}
		all = append(all, commit{sha: sha, at: at})
	}
	if len(all) == 0 {
		return 0, nil
	}

	// The split point: the last commit older than cutoff. Its tree becomes the
	// base, so nothing it contained is lost — only the steps that produced it.
	split := -1
	for i, c := range all {
		if c.at.Before(cutoff) {
			split = i
		}
	}
	// Nothing old enough, or only the root is old enough and folding one commit
	// into one commit achieves nothing.
	if split < 1 {
		return 0, nil
	}

	tree, err := runGit(ctx, r.Dir, "rev-parse", all[split].sha+"^{tree}")
	if err != nil {
		return 0, err
	}
	parent, err := runGit(ctx, r.Dir, "commit-tree", strings.TrimSpace(tree), "-m", msg)
	if err != nil {
		return 0, err
	}
	parent = strings.TrimSpace(parent)

	for _, c := range all[split+1:] {
		t, terr := runGit(ctx, r.Dir, "rev-parse", c.sha+"^{tree}")
		if terr != nil {
			return 0, terr
		}
		subj, serr := runGit(ctx, r.Dir, "log", "-1", "--format=%B", c.sha)
		if serr != nil {
			return 0, serr
		}
		// Author and committer identity and dates are carried over, so the
		// rebuilt history still says who synced and when.
		env, eerr := commitIdentity(ctx, r.Dir, c.sha)
		if eerr != nil {
			return 0, eerr
		}
		next, cerr := runGitStdin(ctx, r.Dir, subj, env,
			"commit-tree", strings.TrimSpace(t), "-p", parent, "-F", "-")
		if cerr != nil {
			return 0, cerr
		}
		parent = strings.TrimSpace(next)
	}

	if _, err = runGit(ctx, r.Dir, "update-ref", "refs/heads/"+branch, parent); err != nil {
		return 0, err
	}
	// Reclaiming the space is the entire point; without this the old objects are
	// still on disk and the repo has not got any smaller.
	_, _ = runGit(ctx, r.Dir, "reflog", "expire", "--expire=now", "--all")
	_, _ = runGit(ctx, r.Dir, "gc", "--prune=now", "--quiet")
	return split, nil
}

// commitIdentity reads a commit's author/committer so a rebuilt copy keeps them.
func commitIdentity(ctx context.Context, dir, sha string) ([]string, error) {
	out, err := runGit(ctx, dir, "log", "-1", "--format=%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI", sha)
	if err != nil {
		return nil, err
	}
	f := strings.Split(strings.TrimSpace(out), "\x1f")
	if len(f) != 6 {
		return nil, fmt.Errorf("unreadable identity for %s", sha)
	}
	return []string{
		"GIT_AUTHOR_NAME=" + f[0], "GIT_AUTHOR_EMAIL=" + f[1], "GIT_AUTHOR_DATE=" + f[2],
		"GIT_COMMITTER_NAME=" + f[3], "GIT_COMMITTER_EMAIL=" + f[4], "GIT_COMMITTER_DATE=" + f[5],
	}, nil
}
