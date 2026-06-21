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
// the removed/kept counts plus the rows it rendered; the caller prints the
// summary. The current branch, the base, and branches still checked out in a
// worktree are skipped — except any in detached, whose worktrees a combined
// sweep just removed (or, in dry-run, would remove), so they're now deletable.
//
// only (when non-nil) restricts the sweep to branches named in it; force names
// branches to delete despite a soft skip reason (not merged, even-with-base, …).
func pruneBranches(ctx context.Context, out io.Writer, repo *gitrepo.Repo, base string, dryRun, gone bool, detached, only, force map[string]bool) (removed, kept int, rows []pruneRow, err error) {
	branches, err := repo.LocalBranches(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	// Branches checked out in a worktree can't be deleted (git refuses); skip
	// them unless their worktree was just freed by a combined sweep.
	inWorktree := worktreeBranches(ctx, repo)
	for name := range detached {
		delete(inWorktree, name)
	}

	// A branch even with base has no commits of its own — it's "merged" only
	// trivially, so never reap it (the brand-new-branch counterpart to the
	// worktree guard).
	baseSHA, _ := repo.RevParse(ctx, base)
	skip := func(label, reason string, forceable bool) {
		kept++
		rows = append(rows, pruneRow{name: label, kind: pruneSkip, state: "skip", why: reason, forceable: forceable})
	}
	for _, b := range branches {
		if b.Current || b.Name == base {
			if only != nil && only[b.Name] {
				skip(b.Name, "current or base branch — can't prune", false)
			}
			continue
		}
		if only != nil && !only[b.Name] {
			continue // not one of the named targets
		}
		forced := force[b.Name]

		// A branch checked out in a worktree can't be deleted on its own; force
		// can't override it here (the worktree must go first — that's the combined
		// sweep's job), so it's not forceable in the branch phase.
		if inWorktree[b.Name] {
			skip(b.Name, "checked out in a worktree", false)
			continue
		}

		remove, reason, forceable := func() (bool, string, bool) {
			// A branch even with base is ambiguous: brand-new (never committed —
			// keep it) or fast-forwarded into base (did real work — reap it). The
			// reflog tells them apart; only the never-advanced case is brand-new.
			ffEqual := false
			if baseSHA != "" {
				if sha, err := repo.RevParse(ctx, b.Name); err == nil && sha == baseSHA {
					advanced, err := repo.BranchAdvanced(ctx, b.Name)
					if err != nil {
						return false, "couldn't read branch history", true
					}
					if !advanced {
						return false, "even with base — nothing to prune", true
					}
					ffEqual = true
				}
			}
			merged, err := repo.IsMerged(ctx, b.Name, base)
			if err != nil {
				return false, "couldn't check merge state", true
			}
			switch {
			case ffEqual && merged:
				return true, "merged (fast-forward) into " + base, false
			case merged:
				return true, "merged into " + base, false
			case b.Gone && gone:
				return true, "upstream gone", false
			case b.Gone:
				return false, "upstream gone — kept (--keep-gone)", true
			default:
				return false, "not merged into " + base, true
			}
		}()
		if !remove && forced && forceable {
			remove, reason = true, reason+" — forced"
		}
		if !remove {
			skip(b.Name, reason, forceable)
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
			skip(b.Name, "delete failed: "+err.Error(), false)
			continue
		}
		removed++
		rows = append(rows, pruneRow{name: b.Name, kind: pruneDone, state: "deleted", why: reason})
	}
	renderPruneTable(out, rows)
	return removed, kept, rows, nil
}
