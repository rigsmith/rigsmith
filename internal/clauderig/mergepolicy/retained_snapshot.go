package mergepolicy

import (
	"bytes"
	"context"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
)

// Ordinary files preserve the selected snapshot verbatim. Equal or unknown
// origin times cannot prove which different snapshot is newer; refuse the tie.
// Both Git parents remain reachable, including the unselected historical copy.
func resolveRetainedSnapshot(ctx context.Context, ours, theirs []byte, files commitartifact.RelatedFiles) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ours == nil || theirs == nil || files == nil {
		return nil, commitartifact.ErrConflict
	}
	if bytes.Equal(ours, theirs) {
		return bytes.Clone(ours), nil
	}
	ourTime, err := files.SnapshotTime(ctx, commitartifact.OurSide)
	if err != nil {
		return nil, err
	}
	theirTime, err := files.SnapshotTime(ctx, commitartifact.TheirSide)
	if err != nil {
		return nil, err
	}
	if ourTime.IsZero() || theirTime.IsZero() || ourTime.Equal(theirTime) {
		return nil, commitartifact.ErrConflict
	}
	selected := theirs
	if ourTime.After(theirTime) {
		selected = ours
	}
	return bytes.Clone(selected), ctx.Err()
}
