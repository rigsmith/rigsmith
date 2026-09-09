package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// PullRequest uses resolved local inputs; Config must be non-nil. Pull retains
// the configured fresh-machine restore behavior and never launches mergetool.
type PullRequest struct {
	Config     *config.Config
	Machine    config.Machine
	StagingDir string
}

// PullResult exposes failures that the SessionStart command deliberately treats
// as best-effort. A failed initial clone creates no journal/staging directory.
type PullResult struct {
	CoordinationError                        error
	RequestError                             error
	CloneError, ReconcileError, RestoreError error
	Repair                                   RepairResult
	Restore                                  *engine.RestoreReport
}

// Pull updates the backup and optionally restores a fresh machine. The command
// keeps its successful exit on these operational failures; callers can inspect
// the result without scraping rendered output.
func (s Service) Pull(ctx context.Context, req PullRequest) (result PullResult) {
	if err := requireCanonicalRunner(ctx); err != nil {
		result.RequestError = err
		s.emit(PullFailed{Err: err})
		return result
	}
	if req.Config == nil {
		result.RequestError = fmt.Errorf("pull requires a configuration")
		s.emit(PullFailed{Err: result.RequestError})
		return result
	}
	ctx, release, err := storelock.Acquire(ctx, req.StagingDir, 0)
	if err != nil {
		result.CoordinationError = err
		s.emit(PullFailed{Err: err})
		return result
	}
	defer release()
	cfg, me, staging := req.Config, req.Machine, req.StagingDir
	// Update the staging repo from the remote (best-effort; never blocks).
	if cfg.Remote != "" {
		if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
			if _, err := gitrepo.Clone(ctx, cfg.Remote, staging); err != nil {
				result.CloneError = err
				// Reported, not journalled. There is no repository yet to
				// carry the record, and writing one would create the
				// staging directory — which git then refuses to clone
				// into, turning one failed clone into every future one
				// failing too.
				s.emit(CloneSkipped{Err: err})
			}
		} else if repo, err := gitrepo.Open(ctx, staging); err == nil {
			// An unfinished merge makes the ff-only pull below fail on every
			// future session, so clear it first rather than reporting the
			// same error forever.
			// A failed repair does not stop this best-effort path. Unlike sync,
			// pull does not capture a new snapshot over the conflicted tree.
			result.Repair = s.RepairMerge(ctx, staging, false)
			if err := repo.Pull(ctx, "origin", "main"); err != nil {
				// A non-ff divergence is not an error to report and forget —
				// it never resolves itself. Merge it here (policies decide,
				// no prompt) so the next sync has one line of history to push.
				// Report the RECONCILE failure, not the ff-only one that sent us
				// here: the ff error is a symptom of divergence, while this one
				// names the path that needs a human and how to finish it — which
				// is the only message that ends the wedge. Journalled too: this is
				// the wedge that hid for a day behind hook stderr.
				if rerr := s.Reconcile(ctx, ReconcileRequest{Repo: repo, Remote: "origin", Branch: "main"}); rerr != nil {
					result.ReconcileError = rerr
					s.emit(PullFailed{Err: rerr})
					_ = journal.Append(staging, journal.Failed(me.Name, journal.OpPull, rerr))
				}
			}
		}
	}

	result.Restore, result.RestoreError = s.autoRestoreIfFresh(ctx, req)
	return result
}

// autoRestoreIfFresh restores onto this machine when AutoRestore is set AND the
// machine is fresh (no projects yet) — so a new computer wires itself up on first
// session without ever clobbering an established one. Best-effort and silent on
// failure (it runs from the SessionStart hook).
func (s Service) autoRestoreIfFresh(ctx context.Context, req PullRequest) (*engine.RestoreReport, error) {
	cfg, me, staging := req.Config, req.Machine, req.StagingDir
	if !cfg.AutoRestore {
		return nil, nil
	}
	cliLoc, st := cfg.RootLocation("cli", me)
	if st != pathmap.StatusResolved {
		return nil, nil
	}
	if entries, err := os.ReadDir(filepath.Join(cliLoc, "projects")); err == nil && len(entries) > 0 {
		return nil, nil // not fresh — never auto-restore over an established machine
	}
	man, err := manifest.Load(staging)
	if err != nil {
		return nil, nil
	}
	rep, rerr := engine.Restore(engine.RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: me, Manifest: man, Prune: cfg.AlwaysPrune,
		Profiles: engine.StagedProfileNames(staging),
	})
	// Journalled either way: this fires once in a machine's life, so it can't
	// bloat the feed, and it's the moment a computer's whole Claude setup
	// arrives — worth a row whether it worked or not. A silent failure here
	// used to leave a fresh machine mysteriously empty.
	_ = journal.Append(staging, journal.FromRestore(me.Name, rep, rerr))
	if rerr != nil {
		return rep, rerr
	}
	s.emit(AutoRestored{DesktopSessions: rep.DesktopSessions()})
	return rep, nil
}
