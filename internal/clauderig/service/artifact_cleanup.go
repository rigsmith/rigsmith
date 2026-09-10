package service

import (
	"context"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
)

// CleanupArtifactWorkspaces explicitly reclaims private scratch for the supplied
// binding. It validates Claude source/staging separation before the shared layer
// acquires writer ownership, then rechecks the binding under those leases.
// Sealed artifacts and recovery intents are retained;
// no queue state, remote, native source or canonical Git bytes change. This
// internal operation is not wired to commands, hooks or automatic startup.
// Inputs must come from trusted local resolution or explicit store selection;
// CaptureBinding describes capture policy, not ownership of private-store paths.
// All stores must be exclusive to this staging directory, as for their writers.
func (s Service) CleanupArtifactWorkspaces(ctx context.Context, binding queue.Binding, inputs QueueInputs) (commitartifact.WorkspaceCleanupResult, error) {
	fail := commitartifact.WorkspaceCleanupResult{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	req, err := workspaceCleanupRequest(binding, inputs)
	if err != nil {
		return fail, err
	}
	return commitartifact.CleanupWorkspaces(ctx, req)
}

// Prepare paths before lock acquisition; mutable staging-backed policy is
// validated again by the callback while cleanup retains writer ownership.
func workspaceCleanupRequest(binding queue.Binding, inputs QueueInputs) (commitartifact.WorkspaceCleanup, error) {
	fail := commitartifact.WorkspaceCleanup{}
	current, err := CaptureBinding(inputs.Sync, inputs.Profiles)
	if err != nil {
		return fail, err
	}
	if current != binding {
		return fail, queue.ErrBinding
	}
	stage, captures, commits, err := artifactStorePaths(ArtifactCaptureRequest{Sync: inputs.Sync, Profiles: inputs.Profiles, Store: inputs.Captures}, inputs.Commits)
	if err != nil {
		return fail, err
	}
	inputs.Captures.Dir, inputs.Commits.Dir = captures, commits
	return commitartifact.WorkspaceCleanup{StagingDir: stage, Captures: inputs.Captures, Commits: inputs.Commits,
		Validate: func(context.Context) error {
			current, err := CaptureBinding(inputs.Sync, inputs.Profiles)
			if err != nil {
				return err
			}
			if current != binding {
				return queue.ErrBinding
			}
			return nil
		},
	}, nil
}

// ReclaimQueueArtifacts explicitly reclaims queue-exclusive private stores. The
// trusted resolver must associate these stores exclusively with q and staging;
// callers must not share them with another queue or independent artifact reader.
// This association is a caller precondition, not checked by CaptureBinding or
// Maintenance. A reset/replacement queue must not reuse the previous stores.
// Queue worker/state ownership and a durable reflush precede staging/artifact
// ownership. Any unfinished work protects all sealed output. Recovery/unknown
// private state also preserves archives. No hooks or commands call this API.
func (s Service) ReclaimQueueArtifacts(ctx context.Context, binding queue.Binding, inputs QueueInputs, q *queue.Queue) (commitartifact.QueueReclamationResult, error) {
	result := commitartifact.QueueReclamationResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if q == nil {
		return result, queue.ErrBinding
	}
	req, err := workspaceCleanupRequest(binding, inputs)
	if err != nil {
		return result, err
	}
	directory, err := canonicalCapturePath(q.Directory())
	if err != nil {
		return result, err
	}
	roots, err := captureRoots(inputs.Sync, inputs.Profiles)
	if err != nil {
		return result, err
	}
	for _, path := range append(mapValues(roots), req.StagingDir, req.Captures.Dir, req.Commits.Dir) {
		if overlapsCapture(directory, path) {
			return result, queue.ErrBinding
		}
	}
	err = q.Maintain(ctx, binding, func(proof *queue.Maintenance) error {
		var err error
		result, err = commitartifact.ReclaimQueueArtifacts(ctx, req, proof)
		return err
	})
	return result, err
}
