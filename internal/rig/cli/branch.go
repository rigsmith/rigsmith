package cli

import (
	"context"
	"io"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// Local-branch helpers behind `rig prune` (and `prune --branches`). Direct
// branch management — create/switch/delete-this-one — is intentionally left to
// git/gh; rig only reaps branches that are *done* (merged or gone-upstream).

// branchStatus is a one-word classification of a local branch, used to label how
// prune sees each branch.
type branchStatus struct {
	tag    string                 // current | worktree | merged | gone | unmerged
	render func(...string) string // lipgloss style for the tag
}

// classifyBranch labels b against base and the set of worktree-attached
// branches. merge state is only computed when needed (it shells out per branch).
func classifyBranch(ctx context.Context, repo *gitrepo.Repo, b gitrepo.Branch, base string, inWorktree map[string]bool) branchStatus {
	switch {
	case b.Current:
		return branchStatus{"current", OkStyle.Render}
	case inWorktree[b.Name]:
		return branchStatus{"worktree", HeaderStyle.Render}
	}
	if merged, err := repo.IsMerged(ctx, b.Name, base); err == nil && merged {
		return branchStatus{"merged", OkStyle.Render}
	}
	if b.Gone {
		return branchStatus{"gone", WarnStyle.Render}
	}
	return branchStatus{"unmerged", DimStyle.Render}
}

// worktreeBranches is the set of branches checked out in a worktree — they can't
// be deleted (git refuses) and belong to the worktree commands. A list failure
// yields an empty set, so callers degrade to "none attached" rather than erroring.
func worktreeBranches(ctx context.Context, repo *gitrepo.Repo) map[string]bool {
	set := map[string]bool{}
	if wts, err := repo.WorktreeList(ctx); err == nil {
		for _, wt := range wts {
			if wt.Branch != "" {
				set[wt.Branch] = true
			}
		}
	}
	return set
}

// pruneBranches deletes local branches that are merged into base or (with gone)
// whose upstream the remote deleted. It prints one line per branch and returns
// the removed/kept counts; the caller prints the summary. The current branch,
// the base, and branches still checked out in a worktree are skipped — except
// any in detached, whose worktrees a combined sweep just removed (or, in
// dry-run, would remove), so they're now deletable.
func pruneBranches(ctx context.Context, out io.Writer, repo *gitrepo.Repo, base string, dryRun, gone bool, detached map[string]bool) (removed, kept int, err error) {
	branches, err := repo.LocalBranches(ctx)
	if err != nil {
		return 0, 0, err
	}
	// Branches checked out in a worktree can't be deleted (git refuses); skip
	// them unless their worktree was just freed by a combined sweep.
	inWorktree := worktreeBranches(ctx, repo)
	for name := range detached {
		delete(inWorktree, name)
	}

	var rows []pruneRow
	skip := func(label, reason string) {
		kept++
		rows = append(rows, pruneRow{name: label, kind: pruneSkip, state: "skip", why: reason})
	}
	for _, b := range branches {
		if b.Current || b.Name == base {
			continue
		}
		if inWorktree[b.Name] {
			skip(b.Name, "checked out in a worktree")
			continue
		}
		merged, err := repo.IsMerged(ctx, b.Name, base)
		if err != nil {
			skip(b.Name, "couldn't check merge state")
			continue
		}
		reason := ""
		switch {
		case merged:
			reason = "merged into " + base
		case b.Gone && gone:
			reason = "upstream gone"
		case b.Gone:
			skip(b.Name, "upstream gone — kept (--keep-gone)")
			continue
		default:
			skip(b.Name, "not merged into "+base)
			continue
		}
		if dryRun {
			removed++
			rows = append(rows, pruneRow{name: b.Name, kind: prunePlan, state: "will delete", why: reason})
			continue
		}
		// Force-delete: we've established the branch is done with, but a
		// squash-merge or gone upstream isn't something `branch -d` recognises.
		if err := repo.DeleteBranch(ctx, b.Name, true); err != nil {
			skip(b.Name, "delete failed: "+err.Error())
			continue
		}
		removed++
		rows = append(rows, pruneRow{name: b.Name, kind: pruneDone, state: "deleted", why: reason})
	}
	renderPruneTable(out, rows)
	return removed, kept, nil
}
