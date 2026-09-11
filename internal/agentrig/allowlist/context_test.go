package allowlist_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
)

func TestCanceledWalkDoesNotReportMissingRootAsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	files, links, err := allowlist.WalkContext(ctx, filepath.Join(t.TempDir(), "missing"), allowlist.List{})
	if !errors.Is(err, context.Canceled) || files != nil || links != nil {
		t.Fatal(files, links, err)
	}
}
