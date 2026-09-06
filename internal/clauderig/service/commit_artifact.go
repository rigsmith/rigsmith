package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// ArtifactCommitRequest consumes a captured queue batch. Capture.Work.CaptureRef
// must belong to its binding and sealed events. Commits is a separate private
// artifact store outside all source, capture and staging roots.
type ArtifactCommitRequest struct {
	Capture ArtifactCaptureRequest
	Commits artifact.Store
}

// CommitArtifact retains a Git snapshot and its full seed ancestry in a durable
// bundle. It never recaptures sources, reads the current login, changes canonical
// staging, pushes, or updates the queue. After success the caller may persist the
// returned reference as the batch's committed phase. A caller already holding a
// committed reference uses commitartifact.Open; it must never rebuild a missing
// committed artifact. Seed objects come only from the capture's retained bundle.
func (s Service) CommitArtifact(ctx context.Context, input ArtifactCommitRequest) (string, error) {
	req, key, err := prepareArtifactRequest(input.Capture, queue.Captured)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(req.Work.CaptureRef, key+":") {
		return "", queue.ErrBinding
	}
	stage, err := canonicalCapturePath(req.Sync.StagingDir)
	if err != nil {
		return "", err
	}
	captures, err := canonicalCapturePath(req.Store.Dir)
	if err != nil {
		return "", err
	}
	commits, err := canonicalCapturePath(input.Commits.Dir)
	if err != nil {
		return "", err
	}
	roots, err := captureRoots(req.Sync, req.Profiles)
	if err != nil {
		return "", err
	}
	for _, path := range append(mapValues(roots), stage, captures) {
		if overlapsCapture(commits, path) {
			return "", fmt.Errorf("commit store must be outside source, capture and staging roots")
		}
	}
	for _, path := range append(mapValues(roots), stage) {
		if overlapsCapture(captures, path) {
			return "", fmt.Errorf("capture store must be outside source and staging roots")
		}
	}
	// The lock graph is capture store -> staging -> commit store. Extraction
	// only reads the capture store, so it does not reverse capture's lock order.
	_, release, err := storelock.Acquire(ctx, stage, StoreWait)
	if err != nil {
		return "", err
	}
	defer release()
	binding, err := artifactPhaseBinding(req, queue.Captured)
	if err != nil {
		return "", err
	}
	if binding != req.Binding {
		return "", queue.ErrBinding
	}
	req.Store.Dir = captures
	input.Commits.Dir = commits
	return commitartifact.Build(ctx, commitartifact.Request{
		Captures: req.Store, Commits: input.Commits, CaptureRef: req.Work.CaptureRef,
		PolicyID:   "claude-retained-commit-v1",
		Message:    adapter.PublicationPlan(req.Sync.Machine.Name, req.Sync.Config.Retention).SnapshotMessage,
		AuthorName: "clauderig", AuthorEmail: "clauderig@localhost",
		Time:    req.Work.Events[len(req.Work.Events)-1].EnqueuedAt,
		Prepare: backupgit.EnsureContext, Audit: engine.CheckPublishContext,
	})
}
