package commitartifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// SnapshotConfirmation verifies an already-published synchronous snapshot. The
// caller owns staging and its private ScratchParent, outside native/backup roots.
// Verify inspects raw committed bytes in a temporary tree and must not mutate it.
// This does not push, trust remote-tracking refs, or change canonical staging.
type SnapshotConfirmation struct {
	Commit, ScratchParent string
	Remote                Transport
	MaxTreeBytes          int64
	Verify                func(context.Context, string) error
}

// ConfirmSnapshot freshly fetches the bound remote into an isolated repository,
// proves the exact snapshot is reachable, and verifies its raw tree. A concurrent
// remote append is fine; missing or rewritten ancestry leaves work unconfirmed.
func ConfirmSnapshot(ctx context.Context, req SnapshotConfirmation) error {
	if !objectID(req.Commit) || !filepath.IsAbs(req.ScratchParent) || req.Remote == nil || req.Verify == nil || req.MaxTreeBytes < 0 {
		return ErrInvalid
	}
	work, err := os.MkdirTemp(req.ScratchParent, ".confirmation-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repo, err := initRepo(ctx, filepath.Join(work, "git"), req.Commit)
	if err != nil {
		return err
	}
	tip, err := req.Remote.Fetch(ctx, repo.dir, remoteRefName)
	if err != nil {
		return err
	}
	if tip == "" {
		return ErrUnconfirmed
	}
	if !objectID(tip) {
		return ErrInvalid
	}
	actual, err := repo.run(ctx, nil, "rev-parse", remoteRefName+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(actual) != tip {
		return ErrInvalid
	}
	if err := repo.completeHistory(ctx); err != nil {
		return err
	}
	contains, err := repo.ancestor(ctx, req.Commit, tip)
	if err != nil {
		return err
	}
	if !contains {
		return ErrUnconfirmed
	}
	return repo.checkTree(ctx, req.Commit, work, req.MaxTreeBytes, req.Verify)
}
