package service

import (
	"context"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/publication"
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
	return s.publication().Reconcile(ctx, req)
}

// FinishMerge audits before committing a pending merge. Native audit and byte
// preparation remain Claude policies.
func FinishMerge(ctx context.Context, repo *gitrepo.Repo) error {
	return (Service{}).publication().FinishMerge(ctx, repo)
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
type ReconcileRequest = publication.ReconcileRequest

// RepairResult distinguishes a repaired or aborted merge from a still-wedged
// store. An error can coexist with Safe when reconciliation aborted the merge.
type RepairResult struct {
	Safe bool
	Err  error
}
