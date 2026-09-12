package service

import (
	"context"
	"fmt"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

// Reconcile brings the staging repo back onto one line of history with the
// remote, and is safe to call when a previous run already left a merge in
// progress — that repair is the point. A staging repo abandoned mid-merge is not
// self-healing: the ff-only pull the SessionStart hook runs fails with "unmerged
// files" every session afterwards, so the wedge outlives whatever caused it until
// someone opens the repo by hand.
//
// Conflicts go through clauderig's policies (see internal/clauderig/mergepolicy)
// rather than to a human, because the merge usually happens where no human is
// watching. Anything the policies decline is handed to git mergetool only when
// allowMergeTool says the caller can afford to block on a person — `sync` can,
// the SessionStart hook cannot. Otherwise the merge is aborted, which leaves the
// repo usable even though the sync did not land.
func (s Service) Reconcile(ctx context.Context, req ReconcileRequest) error {
	repo, remote, branch := req.Repo, req.Remote, req.Branch
	if !repo.InMerge(ctx) {
		conflicted, err := repo.FetchMergeUncommitted(ctx, remote, branch)
		if err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
		if !conflicted {
			return FinishMerge(ctx, repo)
		}
	} else {
		s.emit(MergePending{})
	}

	rep, err := mergepolicy.Resolve(ctx, repo)
	if err != nil {
		return fmt.Errorf("resolve conflicts: %w", err)
	}
	s.emit(ConflictsResolved{Resolutions: rep.Resolved})
	if n := len(rep.Unresolved); n > 0 {
		if !req.AllowMergeTool {
			_ = repo.AbortMerge(ctx)
			return fmt.Errorf("%d conflict(s) need a human (%s); re-run `clauderig sync` in a terminal to resolve via git mergetool",
				n, rep.Unresolved[0])
		}
		s.emit(MergeToolStarting{Count: n})
		if err := repo.RunMergeTool(ctx); err != nil {
			_ = repo.AbortMerge(ctx)
			return fmt.Errorf("mergetool: %w", err)
		}
	}
	return FinishMerge(ctx, repo)
}

// FinishMerge audits a pending merge before committing. Refused merges remain
// resumable. Working files must match the index so cleaning only a working copy
// cannot hide a credential still staged for commit.
func FinishMerge(ctx context.Context, repo *gitrepo.Repo) error {
	merging := repo.InMerge(ctx)
	if merging {
		dirty, err := repo.HasUnstagedChanges(ctx)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("merge has unstaged changes; stage the intended resolutions before retrying")
		}
	}
	root, err := repo.Toplevel(ctx)
	if err != nil {
		return err
	}
	if merging {
		if err := backupgit.Prepare(ctx, root, "ClaudeRig"); err != nil {
			return err
		}
	}
	if err = engine.CheckPublish(root); err != nil {
		return err
	}
	if !merging {
		return nil
	}
	return repo.CommitMerge(ctx)
}

// RepairMerge finishes a merge an earlier run left in progress, before
// anything else touches the staging repo. Order matters: sync commits by staging
// the whole tree, and `git add -A` over a conflicted tree marks the conflicts
// resolved with their `<<<<<<<` markers still in the files — so a repo left
// mid-merge does not just block the next sync, it is one commit away from
// publishing corrupted transcripts and settings to every other machine. Repairing
// first makes that unreachable.
//
// It reports whether the repo is safe to write into afterwards. Callers that go
// on to STAGE and COMMIT must stop when it is not: `git add -A` over a still
// conflicted index marks the conflicts resolved with their markers intact, so
// continuing would publish exactly what this function exists to prevent — and a
// failure here does not always end in an abort (a failed CommitMerge leaves the
// merge standing). Pull does not capture a new snapshot over the conflicted
// tree; it retains its best-effort behavior instead of blocking SessionStart.
func (s Service) RepairMerge(ctx context.Context, staging string, allowMergeTool bool) (result RepairResult) {
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return RepairResult{Safe: true} // no staging repo yet — nothing to wedge
	}
	if !repo.InMerge(ctx) {
		return RepairResult{Safe: true}
	}
	result.Err = s.Reconcile(ctx, ReconcileRequest{Repo: repo, Remote: "origin", Branch: "main", AllowMergeTool: allowMergeTool})
	if result.Err != nil {
		s.emit(MergeRepairFailed{Err: result.Err})
	}
	result.Safe = !repo.InMerge(ctx)
	return result
}

// ReconcileRequest carries the caller's explicit permission to invoke mergetool.
// Background callers must leave AllowMergeTool false.
type ReconcileRequest struct {
	Repo           *gitrepo.Repo
	Remote, Branch string
	AllowMergeTool bool
}

// RepairResult distinguishes a repaired or aborted merge from a still-wedged
// store. An error can coexist with Safe when reconciliation aborted the merge.
type RepairResult struct {
	Safe bool
	Err  error
}
