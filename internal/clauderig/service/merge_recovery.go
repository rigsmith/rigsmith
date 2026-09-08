package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// Each immutable batch and service phase owns a separate repair checkpoint.
// A saved checkpoint is consulted even after MERGE_HEAD disappears; otherwise
// a restart could accept unrelated HEAD as completion of a previous repair.
func artifactMergeStore(dir, key string, limit int64) commitartifact.MergeStageStore {
	return commitartifact.MergeStageStore{Dir: filepath.Join(dir, "merge-recovery-"+key), MaxBytes: limit}
}

func hasArtifactMerge(store commitartifact.MergeStageStore) (bool, error) {
	info, err := os.Lstat(store.Dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, commitartifact.ErrInvalid
	}
	return true, nil
}

// The caller has validated provenance and owns canonical staging. Already
// staged manual resolutions retain the existing completion path; only unresolved
// supported content conflicts enter the sealed automated repair protocol.
func recoverArtifactMerge(ctx context.Context, stage string, store commitartifact.MergeStageStore, p commitartifact.MergeFinishPolicy) (string, error) {
	p.Validate, p.Audit = backupgit.ValidateTree, engine.CheckPublishContext
	policy := commitartifact.MergeStagePolicy{PolicyID: "claude-merge-recovery-v1", MergePlanPolicy: commitartifact.MergePlanPolicy{
		MergeFinishPolicy: p, Resolve: resolveArtifactConflict,
	}}
	saved, err := hasArtifactMerge(store)
	if err != nil {
		return "", err
	}
	if saved {
		return store.Complete(ctx, stage, policy)
	}
	head, err := commitartifact.FinishStagedMerge(ctx, stage, p)
	if errors.Is(err, commitartifact.ErrConflict) {
		return store.Complete(ctx, stage, policy)
	}
	return head, err
}
