// Package service runs codexrig's workflows without cobra or terminal
// rendering: capture, publish, pull, reconcile. The command layer renders; this
// layer decides.
//
// The split exists so the same workflow can be driven from a hook, from a
// terminal and from a test without three copies of the ordering rules — and the
// ordering rules here are load-bearing. A merge repaired in the wrong order
// publishes conflict markers to every machine; a push that skips the tripwire
// publishes whatever a merge brought in.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/devices"
	"github.com/rigsmith/rigsmith/internal/codexrig/engine"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/mergepolicy"
)

// Branch and remote names. Fixed rather than configurable: a sync repo is
// codexrig's own, and a knob here buys nothing but a way for two machines to
// disagree about where the snapshot lives.
const (
	Remote = "origin"
	Branch = "main"
	// TrackingRef is what status compares against.
	TrackingRef = Remote + "/" + Branch
)

// pushAttempts bounds the reconcile-and-retry loop. Three is enough for a normal
// race and short enough that a genuinely wedged remote fails visibly instead of
// spinning.
const pushAttempts = 3

// Event is something worth telling the user about, emitted as the work happens
// rather than returned at the end, so a slow sync shows progress.
type Event interface{ serviceEvent() }

type event struct{}

func (event) serviceEvent() {}

type (
	// SyncStarted marks the beginning of the capture pass.
	SyncStarted struct{ event }
	// Captured carries the capture report.
	Captured struct {
		event
		Report *engine.Report
	}
	// MergePending says a merge was found in progress and is being settled.
	MergePending struct{ event }
	// ConflictsResolved reports what the merge policy settled.
	ConflictsResolved struct {
		event
		Report mergepolicy.Report
	}
	// Published reports the commit/push outcome.
	Published struct {
		event
		Committed bool
		Pushed    bool
		LocalOnly bool
	}
	// PullFailed reports a transport problem that did not stop the session.
	PullFailed struct {
		event
		Err error
	}
	// Restored carries a restore report.
	Restored struct {
		event
		Report *engine.RestoreReport
	}
)

// Service runs the workflows. The function fields are injected so a test can
// drive it without a clock or a real login.
type Service struct {
	Observe func(Event)
	Now     func() time.Time
	// ReadIdentity reports the login this machine is syncing as, identity only.
	// Never a token: this value is written into the synced device registry.
	ReadIdentity func() (devices.Account, error)
}

func (s Service) emit(e Event) {
	if s.Observe != nil {
		s.Observe(e)
	}
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SyncRequest is one capture-and-publish run.
//
// The caller must already hold the sync lock: the engine takes none, and two
// concurrent runs would walk and stage the same tree.
type SyncRequest struct {
	Config     *config.Config
	Machine    config.Machine
	StagingDir string
	// CodexVersion is stamped into the manifest and the device registry.
	CodexVersion string
	DryRun       bool
	// Flush names rollouts the large-file throttle must not defer this run.
	Flush []string
}

// SyncResult is what a run produced.
type SyncResult struct {
	Capture   *engine.Report
	Committed bool
	Pushed    bool
}

// Sync captures the machine's Codex setup into the staging repo and publishes it.
func (s Service) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	var out SyncResult

	// A merge left half-finished is repaired FIRST, and a sync stops when it
	// cannot be. `git add -A` over a conflicted index marks conflicts resolved
	// with the markers still in the files, which is one commit away from
	// publishing a corrupted config to every machine.
	if repair := s.RepairMerge(ctx, req.StagingDir); repair.Err != nil {
		return out, repair.Err
	} else if !repair.Safe {
		return out, errors.New("a merge is in progress that codexrig could not settle; run `codexrig sync` in a terminal to finish it")
	}

	s.emit(SyncStarted{})
	rep, serr := engine.Sync(engine.Options{
		StagingDir:     req.StagingDir,
		Config:         req.Config,
		Machine:        req.Machine,
		CodexVersion:   req.CodexVersion,
		RetentionDays:  req.Config.Retention.HistoryDays,
		RedactRollouts: req.Config.RedactTranscripts,
		MaxFileBytes:   req.Config.Retention.MaxFileBytes,
		LargeFileBytes: req.Config.Retention.LargeFileBytes,
		Flush:          req.Flush,
	})
	out.Capture = rep
	s.emit(Captured{Report: rep})

	// The journal record is written BEFORE the commit, so it rides the sync it
	// describes rather than waiting for the next one. That includes the record
	// of a refusal, which is the whole reason the journal exists: a hook-driven
	// sync that refuses has nowhere else to say so.
	rec := recordFor(req.Machine.Name, rep, serr)
	if jerr := journal.Append(req.StagingDir, rec); jerr != nil {
		// A journal that cannot be written must never cost anyone a backup.
		_ = jerr
	}
	if serr != nil {
		return out, serr
	}

	// Identity for the device registry: read once, and only recorded when this
	// machine has a stable name. Registering a placeholder puts a ghost device
	// in a registry every other machine then has to look at.
	if config.IdentityResolved(req.Config) {
		var acct *devices.Account
		if s.ReadIdentity != nil {
			if a, err := s.ReadIdentity(); err == nil && (a.Email != "" || a.AccountID != "") {
				acct = &a
			}
		}
		reg, lerr := devices.Load(req.StagingDir)
		if lerr == nil {
			reg.Touch(req.Machine.Name, req.Machine.OS, req.CodexVersion, acct, s.now())
			_ = reg.Save(req.StagingDir)
		}
	}

	if req.DryRun {
		return out, nil
	}
	pub, perr := s.Publish(ctx, PublishRequest{
		StagingDir:  req.StagingDir,
		Remote:      req.Config.Remote,
		MachineName: req.Machine.Name,
	})
	out.Committed, out.Pushed = pub.Committed, pub.Pushed
	return out, perr
}

func recordFor(machine string, rep *engine.Report, err error) journal.Record {
	rec := journal.Record{At: time.Now().UTC(), Machine: machine, Op: journal.OpSync, Outcome: journal.OutcomeOK}
	if rep != nil {
		for _, rr := range rep.Roots {
			rec.Files += rr.Files
			rec.Unchanged += rr.Unchanged
			rec.Redactions += rr.Redactions
			rec.Skipped += rr.Skipped
			rec.Deferred += rr.Deferred
			rec.Disallowed += rr.Disallowed
			rec.AgedOut += rr.AgedOut
			rec.Oversize += len(rr.Oversize)
			for _, f := range rr.Redacted {
				rec.RedactedFiles = append(rec.RedactedFiles, journal.RedactedFile{
					Path: rr.ID + "/" + f.Rel, Kinds: f.Kinds, Paths: f.Paths, Count: f.Count,
				})
			}
		}
		rec.AgedOut += rep.RetentionPruned
		for _, f := range rep.Findings {
			rec.Leaks = append(rec.Leaks, journal.Leak{Path: f.Path, Kind: f.Kind, File: f.File})
		}
	}
	switch {
	case err != nil && rep != nil && len(rep.Findings) > 0:
		rec.Outcome, rec.Error = journal.OutcomeRefused, err.Error()
	case err != nil:
		rec.Outcome, rec.Error = journal.OutcomeFailed, err.Error()
	}
	return rec
}

// PublishRequest is one commit-and-push.
type PublishRequest struct {
	StagingDir  string
	Remote      string
	MachineName string
}

// PublishResult says what happened.
type PublishResult struct {
	Committed bool
	Pushed    bool
}

// Publish commits the staging tree and pushes it, reconciling and retrying when
// the remote has moved.
//
// It always attempts a push, even with nothing new to commit, so a machine whose
// previous push failed recovers on its next run rather than waiting for a change.
func (s Service) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	var out PublishResult
	repo, err := gitrepo.Init(ctx, req.StagingDir)
	if err != nil {
		return out, err
	}
	if req.Remote != "" && !repo.HasRemote(ctx, Remote) {
		if err := repo.SetRemote(ctx, Remote, req.Remote); err != nil {
			return out, err
		}
	}
	// The tripwire again, over the tree as it stands. The scan that ran during
	// capture saw what capture staged; this one sees what a merge may have
	// brought in since.
	if err := engine.CheckPublish(req.StagingDir); err != nil {
		return out, err
	}
	changed, err := repo.Commit(ctx, "codexrig sync: "+req.MachineName)
	if err != nil {
		return out, err
	}
	out.Committed = changed

	if req.Remote == "" {
		s.emit(Published{Committed: changed, LocalOnly: true})
		return out, nil
	}
	for attempt := 1; ; attempt++ {
		if err := engine.CheckPublish(req.StagingDir); err != nil {
			return out, err
		}
		perr := repo.Push(ctx, Remote, Branch)
		if perr == nil {
			out.Pushed = true
			s.emit(Published{Committed: changed, Pushed: true})
			return out, nil
		}
		if attempt >= pushAttempts {
			return out, fmt.Errorf("push after reconcile: %w", perr)
		}
		if rerr := s.Reconcile(ctx, repo); rerr != nil {
			return out, rerr
		}
	}
}

// Reconcile fetches, merges, and settles what the policy can.
func (s Service) Reconcile(ctx context.Context, repo *gitrepo.Repo) error {
	conflicted, err := repo.FetchMergeUncommitted(ctx, Remote, Branch)
	if err != nil {
		return err
	}
	if !conflicted {
		return s.finishMerge(ctx, repo)
	}
	s.emit(MergePending{})
	rep, err := mergepolicy.Resolve(ctx, repo)
	if err != nil {
		return err
	}
	s.emit(ConflictsResolved{Report: rep})
	if len(rep.Unresolved) > 0 {
		_ = repo.AbortMerge(ctx)
		return fmt.Errorf("%d file(s) need a person:\n%s", len(rep.Unresolved), rep.Describe())
	}
	return s.finishMerge(ctx, repo)
}

// finishMerge commits a settled merge, but only after the tripwire has read the
// merged tree. Committing first would put the check after the point where the
// other machine's content is already in this machine's history.
func (s Service) finishMerge(ctx context.Context, repo *gitrepo.Repo) error {
	if !repo.InMerge(ctx) {
		return nil
	}
	top, err := repo.Toplevel(ctx)
	if err != nil {
		return err
	}
	if err := engine.CheckPublish(top); err != nil {
		return err
	}
	return repo.CommitMerge(ctx)
}

// RepairResult reports whether the staging repo is safe to work in.
type RepairResult struct {
	Safe bool
	Err  error
}

// RepairMerge finishes or abandons a merge left half-done by an earlier run.
func (s Service) RepairMerge(ctx context.Context, staging string) RepairResult {
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		// No repo yet is not a wedged repo.
		return RepairResult{Safe: true}
	}
	if !repo.InMerge(ctx) {
		return RepairResult{Safe: true}
	}
	s.emit(MergePending{})
	rep, err := mergepolicy.Resolve(ctx, repo)
	if err != nil {
		return RepairResult{Err: err}
	}
	s.emit(ConflictsResolved{Report: rep})
	if len(rep.Unresolved) > 0 {
		return RepairResult{Safe: false}
	}
	if err := s.finishMerge(ctx, repo); err != nil {
		return RepairResult{Err: err}
	}
	return RepairResult{Safe: true}
}

// PullRequest is one fetch-and-maybe-restore.
type PullRequest struct {
	Config     *config.Config
	Machine    config.Machine
	StagingDir string
}

// PullResult reports what a pull managed, without failing on any of it.
type PullResult struct {
	CloneErr   error
	FetchErr   error
	RestoreErr error
	Restore    *engine.RestoreReport
}

// Pull brings the remote's work down, and on a FRESH machine optionally restores
// it.
//
// Nothing here is fatal. It runs from a session-start hook, and a session must
// begin whether or not the network is up — a backup tool that stops someone
// working is worse than one that is a few minutes behind.
func (s Service) Pull(ctx context.Context, req PullRequest) PullResult {
	var out PullResult
	if req.Config.Remote == "" {
		return out
	}
	repo, err := gitrepo.Open(ctx, req.StagingDir)
	if err != nil {
		if _, cerr := gitrepo.Clone(ctx, req.Config.Remote, req.StagingDir); cerr != nil {
			out.CloneErr = cerr
			s.emit(PullFailed{Err: cerr})
			return out
		}
		repo, err = gitrepo.Open(ctx, req.StagingDir)
		if err != nil {
			out.CloneErr = err
			return out
		}
	}
	if repair := s.RepairMerge(ctx, req.StagingDir); repair.Err != nil || !repair.Safe {
		// A pull captures nothing, so an unsettled merge costs it nothing
		// either. Say so and carry on.
		s.emit(PullFailed{Err: errors.New("a merge is in progress; run `codexrig sync` in a terminal")})
		return out
	}
	if err := s.Reconcile(ctx, repo); err != nil {
		out.FetchErr = err
		s.emit(PullFailed{Err: err})
		return out
	}

	if req.Config.AutoRestore && freshMachine(req) {
		man, merr := manifest.Load(req.StagingDir)
		if merr != nil {
			return out
		}
		rep, rerr := engine.Restore(engine.RestoreOptions{
			StagingDir: req.StagingDir, Config: req.Config, Machine: req.Machine, Manifest: man,
		})
		out.Restore, out.RestoreErr = rep, rerr
		if rerr == nil {
			s.emit(Restored{Report: rep})
			_ = journal.Append(req.StagingDir, journal.Succeeded(req.Machine.Name, journal.OpRestore))
		}
	}
	return out
}

// freshMachine reports whether this machine has a Codex setup yet.
//
// Auto-restore fires only here, and never over an established machine. A backup
// tool that quietly rewrites a working setup in the background during session
// start is a tool nobody can trust with the setup.
func freshMachine(req PullRequest) bool {
	loc, status := req.Config.RootLocation(config.RootCLI, req.Machine)
	if status != pathmap.StatusResolved {
		// A root this machine cannot even place is not a machine to restore
		// onto unasked.
		return false
	}
	// "Fresh" means no configuration, not an absent directory: Codex creates its
	// home on first run and leaves it nearly empty, and treating that as
	// established would make auto-restore never fire on exactly the machine it
	// was written for.
	if _, err := os.Stat(codexhome.Config(loc)); err == nil {
		return false
	}
	if entries, err := os.ReadDir(codexhome.Skills(loc)); err == nil && len(entries) > 0 {
		return false
	}
	return true
}
