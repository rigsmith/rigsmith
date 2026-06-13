package commands

import (
	"context"
	"fmt"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/commitsource"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/plugin"
)

// LoadChangesets resolves the changesets a version/status run plans from,
// honoring the configured versioning source: on-disk changeset files
// ("changesets", the default), conventional commits ("commits"), or both. It is
// the single seam where commit-based versioning diverges — everything
// downstream (planner, cascade, grouping, prerelease, changelog) is shared,
// because commit mode produces ordinary in-memory changesets.
//
// fromCommits reports whether any changeset in the result was synthesized from a
// commit (so the caller can skip changeset-file bookkeeping like deletion and
// file-based changelog enrichment that only applies to on-disk changesets).
func (w *Workspace) LoadChangesets(ctx context.Context, pkgs []plugin.Package) (sets []*changeset.Changeset, fromCommits bool, err error) {
	if w.Config.UsesChangesets() {
		onDisk, err := changeset.Dir(w.ChangesetDir, "")
		if err != nil {
			return nil, false, fmt.Errorf("reading changesets: %w", err)
		}
		sets = onDisk
	}
	if w.Config.UsesCommits() {
		derived, err := w.commitChangesets(ctx, pkgs)
		if err != nil {
			return nil, false, err
		}
		sets = append(sets, derived...)
		fromCommits = len(derived) > 0 || w.Config.CommitSource() == config.SourceCommits
	}
	return sets, fromCommits, nil
}

// commitChangesets synthesizes changesets from the commits since each package's
// last release tag. The since-ref is per-package (each module carries its own
// tag, e.g. `core/v1.2.0` vs `v1.2.0`), so packages released at different times
// each see only their own new commits. Packages sharing a since-ref share one
// `git log`.
func (w *Workspace) commitChangesets(ctx context.Context, pkgs []plugin.Package) ([]*changeset.Changeset, error) {
	// Bucket packages by their since-ref so each distinct ref is logged once.
	refOf := map[string]string{}
	pkgsByRef := map[string][]string{}
	for _, p := range pkgs {
		ref := ""
		if v, ok := gitutil.LatestModuleVersion(ctx, w.Root, p.Dir); ok {
			ref = gitutil.ModuleTag(p.Dir, v)
		}
		refOf[p.Name] = ref
		pkgsByRef[ref] = append(pkgsByRef[ref], p.Name)
	}

	var out []*changeset.Changeset
	for ref, names := range pkgsByRef {
		commits, err := gitutil.LogSince(ctx, w.Root, ref)
		if err != nil {
			return nil, fmt.Errorf("reading commits since %q: %w", ref, err)
		}
		// Attribute against the full package set (so deepest-package wins), then
		// keep only the releases for packages whose since-ref is this ref — a
		// commit must not bump a package across a different release window.
		want := make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
		for _, cs := range commitsource.Synthesize(commits, pkgs, w.Root, w.Config) {
			var kept []changeset.Release
			for _, r := range cs.Releases {
				if want[r.Name] {
					kept = append(kept, r)
				}
			}
			if len(kept) == 0 {
				continue
			}
			clone := *cs
			clone.Releases = kept
			out = append(out, &clone)
		}
	}
	return out, nil
}
