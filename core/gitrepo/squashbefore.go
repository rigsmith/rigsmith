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
	// The tip this rebuild is based on. The ref is only moved if it is still
	// here at the end: a sync committing while this runs would otherwise have
	// its commit dropped by the update-ref below.
	was, err := r.Head(ctx)
	if err != nil {
		return 0, err
	}
	// Oldest-first, so the rebuild below can walk parents forward.
	out, err := runGit(ctx, r.Dir, "rev-list", "--reverse", "--format=%H%x1f%cI%x1f%P", "--no-commit-header", "HEAD")
	if err != nil {
		return 0, err
	}
	type commit struct {
		sha     string
		at      time.Time
		parents []string
	}
	var all []commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\x1f")
		if len(fields) < 2 {
			continue
		}
		sha, iso := fields[0], fields[1]
		var parents []string
		if len(fields) > 2 {
			parents = strings.Fields(fields[2])
		}
		at, perr := time.Parse(time.RFC3339, iso)
		if perr != nil {
			// A commit whose date cannot be read must not be silently folded
			// away — treat it as recent and keep it.
			at = time.Now()
		}
		all = append(all, commit{sha: sha, at: at, parents: parents})
	}
	if len(all) == 0 {
		return 0, nil
	}

	// The split point: the end of the leading run of commits that are ALL older
	// than the cutoff. Its tree becomes the base, so nothing it contained is
	// lost — only the steps that produced it.
	//
	// Not the last old commit anywhere in the list. Commit dates are not
	// monotonic here: machines sync on their own clocks, and a merge brings in
	// commits dated around whenever the other machine wrote them. Folding up to
	// a stray old commit that lands after a recent one would fold the recent one
	// away too, inside the window it was promised. Stopping at the first commit
	// in the window keeps more than strictly necessary, which is the safe way to
	// be wrong.
	split := -1
	for i, c := range all {
		if !c.at.Before(cutoff) {
			break
		}
		split = i
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
	base := parent

	// Old sha → its rebuilt copy, so a merge commit inside the retained window
	// keeps every parent. Rebuilding each commit with a single parent flattens
	// those merges, and the history that survives a prune then no longer
	// describes how the machines actually reconciled — which is the question
	// the retained window exists to answer, and what a later merge-base has to
	// read.
	rebuilt := map[string]string{}
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
		// A parent that was folded away resolves to the base commit, which
		// holds its tree — so the ancestry still leads somewhere real.
		args := []string{"commit-tree", strings.TrimSpace(t)}
		for _, p := range mappedParents(c.parents, rebuilt, base) {
			args = append(args, "-p", p)
		}
		args = append(args, "-F", "-")
		next, cerr := runGitStdin(ctx, r.Dir, subj, env, args...)
		if cerr != nil {
			return 0, cerr
		}
		parent = strings.TrimSpace(next)
		rebuilt[c.sha] = parent
	}

	// Compare-and-swap. A sync that committed while this was rebuilding leaves
	// the branch somewhere this rebuild never saw, and moving the ref anyway
	// would delete that commit.
	if _, err = runGit(ctx, r.Dir, "update-ref", "refs/heads/"+branch, parent, was); err != nil {
		return 0, fmt.Errorf("the branch moved while history was being rebuilt — nothing was changed: %w", err)
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

// mappedParents translates a commit's parents into the rebuilt history: a parent
// that was itself rebuilt maps to its copy, and one that was folded away maps to
// the base commit holding its tree. Order is preserved (git treats the first
// parent as the mainline) and duplicates are dropped, which is what several
// folded parents collapse into.
func mappedParents(parents []string, rebuilt map[string]string, base string) []string {
	if len(parents) == 0 {
		return []string{base}
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range parents {
		mapped, ok := rebuilt[p]
		if !ok {
			mapped = base
		}
		if seen[mapped] {
			continue
		}
		seen[mapped] = true
		out = append(out, mapped)
	}
	return out
}
