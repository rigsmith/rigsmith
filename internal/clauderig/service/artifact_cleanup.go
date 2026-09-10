package service

import (
	"context"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
)

// CleanupArtifactWorkspaces explicitly reclaims private scratch for the supplied
// binding. It validates Claude source/staging separation before the shared layer
// acquires writer ownership. Sealed artifacts and recovery intents are retained;
// no queue state, remote, native source or canonical Git bytes change. This
// internal operation is not wired to commands, hooks or automatic startup.
func (s Service) CleanupArtifactWorkspaces(ctx context.Context, binding queue.Binding, inputs QueueInputs) (commitartifact.WorkspaceCleanupResult, error) {
	fail := commitartifact.WorkspaceCleanupResult{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
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
	return commitartifact.CleanupWorkspaces(ctx, commitartifact.WorkspaceCleanup{StagingDir: stage, Captures: inputs.Captures, Commits: inputs.Commits})
}
