package commands

import (
	"context"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// PendingRelease is what `version` would release from changeset FILES: the
// plan built from the .changeset/*.md files not yet consumed (and, on the run
// after `pre exit`, the prerelease's), reflecting prerelease mode as version
// does. Empty when there is nothing to version.
//
// Changesets synthesized from commits are left out on purpose. In commit-based
// versioning a release's own commits stay "unreleased" until its tag exists,
// and a release pipeline publishes before it tags, so counting them would find
// every release pending at the moment it is published.
//
// Publish asks this so it can refuse to ship the versions already on disk while
// changesets wait to bump them: that publish re-pushes the last release, which
// a registry quietly skips as a duplicate, and reads as a successful release.
func PendingRelease(ctx context.Context, ws *Workspace, pkgs []plugin.Package) ([]*planner.Module, error) {
	if !ws.Config.UsesChangesets() {
		return nil, nil
	}
	sets, _, err := ws.LoadChangesets(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	files := make([]*changeset.Changeset, 0, len(sets))
	for _, cs := range sets {
		if cs.Commit == "" {
			files = append(files, cs)
		}
	}
	return assemblePlan(ctx, ws, files, pkgs)
}
