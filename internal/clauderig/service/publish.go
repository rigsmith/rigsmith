package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

const configHistoryMaxCommits = 200
const pushAttempts = 3

// PublishRequest describes an already captured store. The caller must settle
// abandoned merges before capture, hold its existing sync lock through Publish,
// and record capture/device metadata before calling this method.
type PublishRequest struct {
	StagingDir, Remote, MachineName string
	Retention                       config.Retention
	AllowMergeTool                  bool
}

// PublishResult records completed phases even if a later phase fails. Committed
// reports a new local snapshot; Pushed includes retrying an older local commit.
type PublishResult struct {
	Committed bool
	Pushed    bool
}

// Publish audits and commits a captured store, retries publication through the
// Claude merge policies, then maintains config history and repository size.
// The caller retains operation journalling and reports the returned error.
func (s Service) Publish(ctx context.Context, req PublishRequest) (result PublishResult, err error) {
	repo, err := gitrepo.Init(ctx, req.StagingDir)
	if err != nil {
		return result, err
	}
	if req.Remote != "" {
		if err := repo.SetRemote(ctx, "origin", req.Remote); err != nil {
			return result, err
		}
	}
	if err := backupgit.Prepare(ctx, req.StagingDir); err != nil {
		return result, err
	}
	if err := engine.CheckPublish(req.StagingDir); err != nil {
		return result, err
	}
	changed, err := repo.Commit(ctx, "clauderig sync: "+req.MachineName)
	if err != nil {
		return result, err
	}
	result.Committed = changed
	if req.Remote == "" {
		s.emit(Published{Result: result, LocalOnly: true})
		return result, nil
	}
	// Always push (even with no new commit) so a previously-failed push
	// recovers; an in-sync push is a cheap no-op. A rejection means the
	// remote advanced, so reconcile and try again — and keep trying a few
	// times, because with several machines syncing on a timer another one
	// can land a push while this one is still merging. Failing there would
	// report a broken sync for a race that resolves itself on the retry.
	for attempt := 0; ; attempt++ {
		if err := backupgit.Validate(ctx, req.StagingDir); err != nil {
			return result, err
		}
		if err := engine.CheckPublish(req.StagingDir); err != nil {
			return result, err
		}
		perr := repo.Push(ctx, "origin", "main")
		if perr == nil {
			break
		}
		if attempt >= pushAttempts {
			return result, fmt.Errorf("push after reconcile: %w", perr)
		}
		if err := s.Reconcile(ctx, ReconcileRequest{Repo: repo, Remote: "origin", Branch: "main", AllowMergeTool: req.AllowMergeTool}); err != nil {
			return result, err
		}
	}
	result.Pushed = true
	s.emit(Published{Result: result})

	// Preserve config history on a separate branch that survives main's
	// squash (everything except the disposable transcript tree). Bounded:
	// squash it once its commit count grows large. Best-effort throughout.
	if changed, cerr := repo.CommitSubtree(ctx, "config-history", []string{".", ":!cli/projects"}, "clauderig config: "+req.MachineName); cerr == nil && changed {
		if repo.BranchCommitCount(ctx, "config-history") > configHistoryMaxCommits {
			if err := repo.SquashBranch(ctx, "config-history", "clauderig: squashed config history"); err == nil && req.Remote != "" {
				_ = repo.ForcePushBranch(ctx, "origin", "config-history")
			}
		} else if req.Remote != "" {
			_ = repo.PushBranch(ctx, "origin", "config-history")
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
	if gitrepo.ShouldSquash(gitBytes, wtBytes, req.Retention.FloorBytes, req.Retention.SquashFactor) {
		s.emit(Repacking{GitBytes: gitBytes, Factor: req.Retention.SquashFactor})
		if err := repo.Repack(ctx); err != nil {
			return result, fmt.Errorf("repack: %w", err)
		}
		gitBytes, _ = repo.GitDirBytes(ctx)
	}

	// Only history's length can still be the problem here, so now it is
	// fair to drop some. Keep whole days, cut on a day boundary: the
	// squash used to fire at whatever o'clock it tripped, which is how a
	// repo came to report that its history began at 08:18 on a Tuesday.
	if gitrepo.ShouldSquash(gitBytes, wtBytes, req.Retention.FloorBytes, req.Retention.SquashFactor) {
		keep := req.Retention.KeepDays()
		cutoff := startOfDay(s.now().AddDate(0, 0, -keep))
		folded, err := repo.SquashBefore(ctx, cutoff,
			"clauderig: history before "+cutoff.Format("2006-01-02"))
		if err != nil {
			return result, fmt.Errorf("squash: %w", err)
		}
		if folded > 0 {
			s.emit(HistoryFolded{Count: folded, Cutoff: cutoff, KeepDays: keep})
			if err := repo.ForcePush(ctx, "origin", "main"); err != nil {
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
