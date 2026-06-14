package planner

import (
	"fmt"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// PartitionChangesets splits changesets into the ones a version run consumes
// (deleted once the run lands) and the ones it keeps: a changeset whose named
// packages are all ignored releases nothing, so it stays on disk for a future
// run where they aren't. Two shapes are hard errors before anything is
// written, matching @changesets (verified against v3.0.0-next.5): a changeset
// naming both ignored and non-ignored packages, and a changeset naming a
// package that isn't in the workspace. An empty changeset (no releases) is
// consumed.
func PartitionChangesets(changesets []*changeset.Changeset, packages []plugin.Package, cfg *config.Config) (consumed, kept []*changeset.Changeset, err error) {
	known := make(map[string]bool, len(packages))
	for _, p := range packages {
		known[p.Name] = true
	}
	for _, cs := range changesets {
		ignored, released := 0, 0
		for _, rel := range cs.Releases {
			if !known[rel.Name] {
				return nil, nil, fmt.Errorf("changeset %s names %q, which is not in the workspace", cs.ID, rel.Name)
			}
			if cfg.IsIgnored(rel.Name) {
				ignored++
			} else {
				released++
			}
		}
		switch {
		case ignored > 0 && released > 0:
			return nil, nil, fmt.Errorf("changeset %s mixes ignored and not-ignored packages; mixed changesets are not allowed", cs.ID)
		case ignored > 0:
			kept = append(kept, cs)
		default:
			consumed = append(consumed, cs)
		}
	}
	return consumed, kept, nil
}
