package service

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
)

// QueueInputs supplies freshly resolved configuration and the producer's saved
// identity for one provenance. Resolve must not substitute the worker's login.
// Remote uses the already configured Git/gh path; callers retain responsibility
// for existing repository privacy checks. All directories must remain stable.
type QueueInputs struct {
	Sync              SyncRequest
	Identity          Identity
	Profiles          []string
	Captures, Commits artifact.Store
	Remote            ArtifactTransport
}

// QueueAdapter connects RunOne to Claude's sealed artifact services. Resolve is
// called once per claimed batch, with its binding and saved provenance ID. It
// must return current inputs, not derive new destinations from old queue work.
// This adapter does not install hooks, run a daemon, update canonical staging,
// acknowledge manual sync coverage, or choose retry timing for unknown errors.
type QueueAdapter struct {
	Service Service
	Resolve func(context.Context, queue.Binding, string) (QueueInputs, error)
}

var _ queue.Adapter = QueueAdapter{}

func (a QueueAdapter) Begin(ctx context.Context, binding queue.Binding, work queue.Work) (queue.Execution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.Resolve == nil || len(work.Events) == 0 || work.Status != queue.Running ||
		(work.Phase != queue.Queued && work.Phase != queue.Captured && work.Phase != queue.Committed) {
		return nil, queue.ErrTransition
	}
	inputs, err := a.Resolve(ctx, binding, work.Events[0].Request.ProvenanceID)
	if err != nil {
		return nil, err
	}
	req, key, err := prepareArtifactRequest(ArtifactCaptureRequest{
		Store: inputs.Captures, Binding: binding, Work: work,
		Sync: inputs.Sync, Identity: inputs.Identity, Profiles: inputs.Profiles,
	}, work.Phase)
	if err != nil {
		return nil, err
	}
	plan := adapter.PublicationPlan(req.Sync.Machine.Name, req.Sync.Config.Retention)
	if inputs.Remote == nil || req.Sync.Config.Remote == "" {
		return nil, queue.ErrBinding // local-only work cannot be acknowledged as pushed
	}
	remote, branch := inputs.Remote.Destination()
	if remote != req.Sync.Config.Remote || branch != plan.Branch {
		return nil, queue.ErrBinding
	}
	stage, captures, commits, err := artifactStorePaths(req, inputs.Commits)
	if err != nil {
		return nil, err
	}
	req.Store.Dir, inputs.Commits.Dir = captures, commits
	staging, release, err := storelock.Acquire(ctx, stage, StoreWait)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	current, err := artifactPhaseBinding(req, work.Phase)
	if err != nil {
		return nil, err
	}
	if current != binding {
		return nil, queue.ErrBinding
	}
	// Never rebuild a phase whose durable marker already exists. Full Git
	// descriptor/history validation follows in CommitArtifact/PublishArtifact.
	switch work.Phase {
	case queue.Queued:
		if work.CaptureRef != "" || work.CommitRef != "" {
			return nil, queue.ErrTransition
		}
	case queue.Captured, queue.Committed:
		if !strings.HasPrefix(work.CaptureRef, key+":") {
			return nil, queue.ErrBinding
		}
		if work.Phase == queue.Captured {
			if work.CommitRef != "" {
				return nil, queue.ErrTransition
			}
			err = req.Store.Verify(ctx, work.CaptureRef)
		} else {
			commitKey, keyErr := commitartifact.RequestKey(claudeCommitRequest(req, inputs.Commits))
			if keyErr != nil {
				return nil, keyErr
			}
			if !strings.HasPrefix(work.CommitRef, commitKey+":") {
				return nil, queue.ErrBinding
			}
			err = inputs.Commits.Verify(ctx, work.CommitRef)
		}
		if err != nil {
			return nil, err
		}
	}
	failed = false
	return &queueExecution{service: a.Service, staging: staging, release: release,
		input: ArtifactPublishRequest{Commit: ArtifactCommitRequest{Capture: req, Commits: inputs.Commits}, Remote: inputs.Remote}}, nil
}

// A single sequential RunOne owns this execution and its detached inputs. Private
// store operations receive the driver's independent context; only the staging
// acquisition borrows staging. Close follows completion of all service children.
type queueExecution struct {
	service Service
	staging context.Context
	release func()
	input   ArtifactPublishRequest
	closed  bool
}

func (e *queueExecution) check(ctx context.Context, work queue.Work, phase queue.Phase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed || work.Phase != phase || !reflect.DeepEqual(work, e.input.Commit.Capture.Work) {
		return queue.ErrTransition
	}
	return nil
}
func (e *queueExecution) Capture(ctx context.Context, work queue.Work) (string, error) {
	if err := e.check(ctx, work, queue.Queued); err != nil {
		return "", err
	}
	ref, err := e.service.captureArtifact(ctx, e.staging, e.input.Commit.Capture)
	if err == nil {
		e.input.Commit.Capture.Work.Phase = queue.Captured
		e.input.Commit.Capture.Work.CaptureRef = ref
	}
	return ref, err
}
func (e *queueExecution) Commit(ctx context.Context, work queue.Work) (string, error) {
	if err := e.check(ctx, work, queue.Captured); err != nil {
		return "", err
	}
	ref, err := e.service.commitArtifact(ctx, e.staging, e.input.Commit)
	if err == nil {
		e.input.Commit.Capture.Work.Phase = queue.Committed
		e.input.Commit.Capture.Work.CommitRef = ref
	}
	return ref, err
}
func (e *queueExecution) Push(ctx context.Context, work queue.Work) error {
	if err := e.check(ctx, work, queue.Committed); err != nil {
		return err
	}
	_, err := e.service.publishArtifact(ctx, e.staging, e.input)
	if err == nil {
		e.input.Commit.Capture.Work.Phase = queue.Pushed
	}
	// Conflicts need a deliberate recovery decision; never loop over the same
	// conflict or silently omit the queued capture. Other errors retain their
	// original type and last durable phase for caller-controlled recovery.
	if errors.Is(err, commitartifact.ErrConflict) {
		return &queue.ExecutionFailure{Code: "publication-conflict", RetryAt: e.service.now(), Blocked: true, Cause: err}
	}
	return err
}
func (e *queueExecution) Close() {
	if !e.closed {
		e.closed = true
		e.release()
	}
}
