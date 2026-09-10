package service

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
)

// CheckQueueStartup is Claude's optional Queue.Run startup check. Callers resolve
// fresh inputs and pass the runner's binding, retaining existing remote privacy
// checks. It does not read identity or native transcript files, claim work, recover
// merges, initialize Git history or push. An uninitialized or unrelated store
// requires explicit foreground initialization/recovery before starting the loop.
// No commands or hooks use this internal entry point yet.
func (s Service) CheckQueueStartup(ctx context.Context, binding queue.Binding, inputs QueueInputs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.CheckSupervisorLease(ctx); err != nil {
		return err
	}
	if inputs.Sync.Config == nil || inputs.Remote == nil || inputs.Sync.Config.Remote == "" {
		return queue.ErrBinding
	}
	remote, branch := inputs.Remote.Destination()
	plan := adapter.PublicationPlan(inputs.Sync.Machine.Name, inputs.Sync.Config.Retention)
	if remote != inputs.Sync.Config.Remote || branch != plan.Branch {
		return queue.ErrBinding
	}
	stage, _, scratch, err := artifactStorePaths(ArtifactCaptureRequest{Sync: inputs.Sync, Profiles: inputs.Profiles, Store: inputs.Captures}, inputs.Commits)
	if err != nil {
		return err
	}
	ctx, release, err := storelock.Acquire(ctx, stage, StoreWait)
	if err != nil {
		return err
	}
	defer release()
	ctx = process.WithSupervisorLease(ctx, ctx)
	current, err := CaptureBinding(inputs.Sync, inputs.Profiles)
	if err != nil {
		return err
	}
	if current != binding {
		return queue.ErrBinding
	}
	head, err := commitartifact.SettledHead(ctx, stage)
	if err != nil {
		return err
	}
	if head == "" {
		return commitartifact.ErrSharedHistory
	}
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return err
	}
	_, err = commitartifact.CheckStartupHistory(ctx, commitartifact.StartupHistoryRequest{
		LocalDir: stage, LocalCommit: head, ScratchParent: scratch, Remote: inputs.Remote,
	})
	return err
}
