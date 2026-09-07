package service

import (
	"context"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

// ArtifactTransport is trusted transport code fixed to one destination/branch.
// Destination must report exactly those immutable values. All Transport rules,
// including ordinary fast-forward pushes and child cleanup, still apply. There
// is deliberately no default transport or authentication lookup here.
type ArtifactTransport interface {
	commitartifact.Transport
	Destination() (remote, branch string)
}

// ArtifactPublishRequest consumes an existing committed batch. Remote must be
// explicitly bound to its configured destination and Claude's native branch.
type ArtifactPublishRequest struct {
	Commit ArtifactCommitRequest
	Remote ArtifactTransport
}

// PublishArtifact applies Claude's bindings, byte-preservation policy and secret
// audit to retained publication. The staging lease spans HEAD inspection through
// final remote confirmation and child cleanup. It never recaptures, reads the
// worker's login, updates canonical staging, or acknowledges queue work. Only a
// confirmed success permits a caller to persist the pushed phase. Manifest/device
// content conflicts use native metadata unions. Other file/structural conflicts,
// canonical merge repair and local-only completion remain separate work.
func (s Service) PublishArtifact(ctx context.Context, input ArtifactPublishRequest) (commitartifact.Publication, error) {
	return s.publishArtifact(ctx, ctx, input)
}

func (s Service) publishArtifact(ctx, staging context.Context, input ArtifactPublishRequest) (commitartifact.Publication, error) {
	fail := commitartifact.Publication{}
	req, key, err := prepareArtifactRequest(input.Commit.Capture, queue.Committed)
	if err != nil {
		return fail, err
	}
	if !strings.HasPrefix(req.Work.CaptureRef, key+":") || req.Work.CommitRef == "" {
		return fail, queue.ErrBinding
	}
	commitKey, err := commitartifact.RequestKey(claudeCommitRequest(req, input.Commit.Commits))
	if err != nil {
		return fail, err
	}
	if !strings.HasPrefix(req.Work.CommitRef, commitKey+":") {
		return fail, queue.ErrBinding
	}
	plan := adapter.PublicationPlan(req.Sync.Machine.Name, req.Sync.Config.Retention)
	if input.Remote == nil || req.Sync.Config.Remote == "" {
		return fail, queue.ErrBinding
	}
	remote, branch := input.Remote.Destination()
	if remote != req.Sync.Config.Remote || branch != plan.Branch {
		return fail, queue.ErrBinding
	}
	stage, _, commits, err := artifactStorePaths(req, input.Commit.Commits)
	if err != nil {
		return fail, err
	}
	_, release, err := storelock.Acquire(staging, stage, StoreWait)
	if err != nil {
		return fail, err
	}
	defer release()
	binding, err := artifactPhaseBinding(req, queue.Committed)
	if err != nil {
		return fail, err
	}
	if binding != req.Binding {
		return fail, queue.ErrBinding
	}
	head, err := commitartifact.SettledHead(ctx, stage)
	if err != nil {
		return fail, err
	}
	localDir := ""
	if head != "" {
		localDir = stage
	}
	input.Commit.Commits.Dir = commits
	return commitartifact.Publish(ctx, commitartifact.PublishRequest{
		Commits: input.Commit.Commits, CommitRef: req.Work.CommitRef, CaptureRef: req.Work.CaptureRef,
		LocalDir: localDir, LocalCommit: head, Remote: input.Remote,
		Message: plan.SnapshotMessage, AuthorName: "clauderig", AuthorEmail: "clauderig@localhost",
		Time: req.Work.Events[len(req.Work.Events)-1].EnqueuedAt, Attempts: plan.PushRetries + 1,
		MaxTreeBytes: input.Commit.Commits.MaxBytes,
		Validate:     backupgit.ValidateTree, Audit: engine.CheckPublishContext,
		Resolve: mergepolicy.ResolveMetadata,
	})
}
