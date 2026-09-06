package service

import (
	"context"
	"fmt"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/publication"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

// PublishRequest describes an already captured store. The caller must settle
// abandoned merges and record capture/device metadata first. Callers composing
// these phases hold a store lease across them; Publish borrows that lease.
type PublishRequest struct {
	StagingDir, Remote, MachineName string
	Retention                       config.Retention
	AllowMergeTool                  bool
}

type PublishResult = publication.PublishResult

// Publish supplies Claude's policies to the shared Git workflow. Journalling and
// native metadata serialization remain in the calling Claude services.
func (s Service) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	ctx, release, err := storelock.Acquire(ctx, req.StagingDir, StoreWait)
	if err != nil {
		return PublishResult{}, err
	}
	defer release()
	return s.publication().Publish(ctx, publication.PublishRequest{
		StagingDir: req.StagingDir, Remote: req.Remote, AllowMergeTool: req.AllowMergeTool,
		Plan: adapter.PublicationPlan(req.MachineName, req.Retention),
	})
}

func (s Service) publication() publication.Workflow {
	return publication.Workflow{
		Now: s.Now,
		Policy: publication.Policy{
			Init:    gitrepo.Init,
			Prepare: backupgit.Prepare, Validate: backupgit.Validate, Audit: engine.CheckPublish,
			Resolve: func(ctx context.Context, repo *gitrepo.Repo) ([]string, error) {
				rep, err := mergepolicy.Resolve(ctx, repo)
				if err != nil {
					return nil, err
				}
				s.emit(ConflictsResolved{Resolutions: rep.Resolved})
				return rep.Unresolved, nil
			},
			HumanRequired: func(paths []string) error {
				return fmt.Errorf("%d conflict(s) need a human (%s); re-run `clauderig sync` in a terminal to resolve via git mergetool", len(paths), paths[0])
			},
		},
		Observe: func(e publication.Event) {
			switch e := e.(type) {
			case publication.Published:
				s.emit(Published{Result: e.Result, LocalOnly: e.LocalOnly})
			case publication.Repacking:
				s.emit(Repacking{GitBytes: e.GitBytes, Factor: e.Factor})
			case publication.HistoryFolded:
				s.emit(HistoryFolded{Count: e.Count, Cutoff: e.Cutoff, KeepDays: e.KeepDays})
			case publication.MergePending:
				s.emit(MergePending{})
			case publication.MergeToolStarting:
				s.emit(MergeToolStarting{Count: e.Count})
			}
		},
	}
}
