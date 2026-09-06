package publication

import (
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// PublishRequest describes an already captured store. The caller holds its
// operation lock, repairs abandoned merges and records metadata before publishing.
type PublishRequest struct {
	StagingDir, Remote string
	Plan               Plan
	AllowMergeTool     bool
}

// PublishResult records completed phases even if a later phase fails.
type PublishResult struct{ Committed, Pushed bool }

// Publish audits and commits a captured store, retries pushes through the supplied
// conflict policy, then maintains history. A failed push is retried even when no
// new commit was created. Publication is reported before maintenance errors.
func (w Workflow) Publish(ctx context.Context, req PublishRequest) (result PublishResult, err error) {
	if err := w.Policy.check(); err != nil {
		return result, err
	}
	if req.Plan.RemoteName == "" || req.Plan.Branch == "" || req.Plan.PushRetries < 0 || req.Plan.Retention.FoldMessage == nil {
		return result, fmt.Errorf("publication: incomplete plan")
	}
	repo, err := w.Policy.Init(ctx, req.StagingDir)
	if err != nil {
		return result, err
	}
	if req.Remote != "" {
		if err := repo.SetRemote(ctx, req.Plan.RemoteName, req.Remote); err != nil {
			return result, err
		}
	}
	if err := w.Policy.Prepare(ctx, req.StagingDir); err != nil {
		return result, err
	}
	if err := w.Policy.Audit(req.StagingDir); err != nil {
		return result, err
	}
	changed, err := repo.Commit(ctx, req.Plan.SnapshotMessage)
	if err != nil {
		return result, err
	}
	result.Committed = changed
	if req.Remote == "" {
		w.emit(Published{Result: result, LocalOnly: true})
		return result, nil
	}
	// Always push (even with no new commit) so a previously-failed push
	// recovers; an in-sync push is a cheap no-op. A rejection means the
	// remote advanced, so reconcile and try again — and keep trying a few
	// times, because with several machines syncing on a timer another one
	// can land a push while this one is still merging. Failing there would
	// report a broken sync for a race that resolves itself on the retry.
	for attempt := 0; ; attempt++ {
		if err := w.Policy.Validate(ctx, req.StagingDir); err != nil {
			return result, err
		}
		if err := w.Policy.Audit(req.StagingDir); err != nil {
			return result, err
		}
		perr := repo.Push(ctx, req.Plan.RemoteName, req.Plan.Branch)
		if perr == nil {
			break
		}
		if attempt >= req.Plan.PushRetries {
			return result, fmt.Errorf("push after reconcile: %w", perr)
		}
		if err := w.Reconcile(ctx, ReconcileRequest{Repo: repo, Remote: req.Plan.RemoteName, Branch: req.Plan.Branch, AllowMergeTool: req.AllowMergeTool}); err != nil {
			return result, err
		}
	}
	result.Pushed = true
	w.emit(Published{Result: result})

	// Preserve selected history on an independent branch. Bound its commit
	// count using the supplied policy. Maintenance remains best-effort.
	if req.Plan.History != nil {
		if changed, cerr := repo.CommitSubtree(ctx, req.Plan.History.Branch, req.Plan.History.Paths, req.Plan.History.CommitMessage); cerr == nil && changed {
			if repo.BranchCommitCount(ctx, req.Plan.History.Branch) > req.Plan.History.MaxCommits {
				if err := repo.SquashBranch(ctx, req.Plan.History.Branch, req.Plan.History.SquashMessage); err == nil && req.Remote != "" {
					_ = repo.ForcePushBranch(ctx, req.Plan.RemoteName, req.Plan.History.Branch)
				}
			} else if req.Remote != "" {
				_ = repo.PushBranch(ctx, req.Plan.RemoteName, req.Plan.History.Branch)
			}
		}
	}

	// Size-based maintenance: bound .git when it has outgrown the content.
	//
	// Repack FIRST, and re-measure. Every sync writes its objects loose
	// and undeltified, and append-only transcripts compress to almost
	// nothing once packed — on a real repo 2.4 GB of a 2.9 GB .git was
	// simply unpacked. Squashing to escape that traded a month of
	// history for something a gc would have given back for free.
	gitBytes, _ := repo.GitDirBytes(ctx)
	wtBytes, _ := repo.WorkTreeBytes(ctx)
	if gitrepo.ShouldSquash(gitBytes, wtBytes, req.Plan.Retention.FloorBytes, req.Plan.Retention.SquashFactor) {
		w.emit(Repacking{GitBytes: gitBytes, Factor: req.Plan.Retention.SquashFactor})
		if err := repo.Repack(ctx); err != nil {
			return result, fmt.Errorf("repack: %w", err)
		}
		gitBytes, _ = repo.GitDirBytes(ctx)
	}

	// Only history's length can still be the problem here, so now it is
	// fair to drop some. Keep whole days, cut on a day boundary: the
	// squash used to fire at whatever o'clock it tripped, which is how a
	// repo came to report that its history began at 08:18 on a Tuesday.
	if gitrepo.ShouldSquash(gitBytes, wtBytes, req.Plan.Retention.FloorBytes, req.Plan.Retention.SquashFactor) {
		keep := req.Plan.Retention.KeepDays
		cutoff := startOfDay(w.now().AddDate(0, 0, -keep))
		folded, err := repo.SquashBefore(ctx, cutoff,
			req.Plan.Retention.FoldMessage(cutoff))
		if err != nil {
			return result, fmt.Errorf("squash: %w", err)
		}
		if folded > 0 {
			w.emit(HistoryFolded{Count: folded, Cutoff: cutoff, KeepDays: keep})
			if err := repo.ForcePush(ctx, req.Plan.RemoteName, req.Plan.Branch); err != nil {
				return result, fmt.Errorf("force-push after squash: %w", err)
			}
		}
	}
	return result, nil
}

// startOfDay is local midnight, preserving the existing retention boundary.
func startOfDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
