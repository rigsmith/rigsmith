package commands

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// PendingRelease is what `version` would release from changeset FILES: the
// plan built from the .changeset/*.md files not yet consumed (and, on the run
// after `pre exit`, the prerelease's), reflecting prerelease mode as version
// does. Empty when there is nothing to version, including when there is no
// .changeset/ directory at all.
//
// It reads the files on disk and nothing else. Commit-derived changesets are
// left out on purpose: in commit-based versioning a release's own commits stay
// "unreleased" until its tag exists, and a release pipeline publishes before it
// tags, so counting them would find every release pending at the moment it is
// published. That includes the synthetic "Initial release" changeset a first
// release collapses its commits into, which carries no commit SHA, so the
// files are read directly rather than picked out of the mixed set by one.
//
// Publish asks this so it can refuse to ship the versions already on disk while
// changesets wait to bump them: that publish re-pushes the last release, which
// a registry quietly skips as a duplicate, and reads as a successful release.
func PendingRelease(ctx context.Context, ws *Workspace, pkgs []plugin.Package) ([]*planner.Module, error) {
	if !ws.Config.UsesChangesets() {
		return nil, nil
	}
	onDisk, err := changeset.Dir(ws.ChangesetDir, "")
	if err != nil {
		// Only a missing directory means "no changesets". A not-exist error for a
		// file inside it (a dangling symlink, say) is a changeset that couldn't be
		// read, and treating it as none would wave the publish through.
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading changesets: %w", err)
		}
		if _, statErr := os.Stat(ws.ChangesetDir); !errors.Is(statErr, fs.ErrNotExist) {
			if statErr != nil {
				return nil, fmt.Errorf("reading changesets: %w", statErr)
			}
			return nil, fmt.Errorf("reading changesets: %w", err)
		}
		onDisk = nil
	}
	files := make([]*changeset.Changeset, 0, len(onDisk))
	for _, cs := range onDisk {
		if ws.since.keeps(cs) {
			files = append(files, cs)
		}
	}
	return assemblePlan(ctx, ws, files, pkgs)
}
