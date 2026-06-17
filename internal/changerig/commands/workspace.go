// Package commands holds the changeset lifecycle commands — init, add, status,
// version, info — as a reusable library. The `changerig` binary exposes exactly
// these; the `shiprig` release tool imports the same builders and layers its
// orchestration verbs (publish/tag/pre) on top, so the two tools can never
// diverge on what a changeset or a version run means.
package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Workspace is the resolved context for a command invocation.
type Workspace struct {
	Root         string
	ChangesetDir string
	Config       *config.Config
	Registry     *plugin.Registry
}

// FindRoot walks up from start to the directory containing a .changeset folder,
// falling back to the nearest .git, then start itself.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	gitFallback := ""
	for {
		if info, err := os.Stat(filepath.Join(dir, ".changeset")); err == nil && info.IsDir() {
			return dir, nil
		}
		if gitFallback == "" {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				gitFallback = dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if gitFallback != "" {
		return gitFallback, nil
	}
	return filepath.Abs(start)
}

// Open resolves the workspace from the current working directory.
func Open() (*Workspace, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := FindRoot(cwd)
	if err != nil {
		return nil, err
	}
	changesetDir := filepath.Join(root, ".changeset")

	cfg := config.Default()
	if c, err := config.Load(changesetDir); err == nil {
		cfg = c
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	return &Workspace{
		Root:         root,
		ChangesetDir: changesetDir,
		Config:       cfg,
		Registry:     ecosystem.Default(),
	}, nil
}

// Initialized reports whether the repo has a changeset config in any of its
// allowed locations (a .changeset file, a root file, or a .rig.json key) — not
// just the canonical .changeset/config.json. An ambiguous setup counts as
// initialized (the next command surfaces the conflict).
func (w *Workspace) Initialized() bool {
	src, err := cfgfind.Find(config.Spec(w.ChangesetDir))
	return err != nil || src != nil
}

// discovered is one package paired with the id of the ecosystem that found it,
// kept during discovery so overlay reconciliation can drop a base package by its
// (ecosystem, directory) identity before the name-keyed ecoOf map is built.
type discovered struct {
	pkg   plugin.Package
	ecoID string
}

// Discover enumerates packages across every ecosystem that applies to the repo,
// returning the packages and a name→ecosystem-id map. Discovery is narrowed to
// the top-level config.Paths roots; a per-ecosystem `sourcePath` block overrides
// those for that ecosystem only. With neither, the whole repo is scanned (minus
// the usual ignores and .gitignored files).
//
// Overlay ecosystems (EcosystemInfo.Overlays — e.g. Tauri over cargo, Electron
// over node) reuse a base ecosystem's manifest but own the release. After every
// adapter has run, reconcileOverlays drops each base package whose directory an
// overlay also claimed, so a desktop app is owned and released once — by the
// overlay — rather than appearing twice.
func (w *Workspace) Discover(ctx context.Context) ([]plugin.Package, map[string]string, error) {
	var found []discovered
	seen := map[string]bool{} // dedupe a package discovered via overlapping roots

	for _, eco := range w.Registry.All() {
		ok, err := eco.Detect(ctx, w.Root)
		if err != nil {
			return nil, nil, fmt.Errorf("detect %s: %w", eco.Info().ID, err)
		}
		if !ok {
			continue
		}
		// A per-ecosystem sourcePath narrows just this ecosystem; otherwise the
		// top-level paths (or "." for the whole repo) apply.
		roots := w.Config.Paths
		if sp := w.Config.EcoConfig(eco.Info().ID).SourcePath; sp != "" {
			roots = []string{sp}
		}
		if len(roots) == 0 {
			roots = []string{"."}
		}
		for _, root := range roots {
			resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: w.Root, SourcePath: root})
			if err != nil {
				return nil, nil, fmt.Errorf("discover %s: %w", eco.Info().ID, err)
			}
			for _, p := range resp.Packages {
				key := eco.Info().ID + "\x00" + p.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, discovered{pkg: p, ecoID: eco.Info().ID})
			}
		}
	}

	found = w.reconcileOverlays(found)

	all := make([]plugin.Package, 0, len(found))
	ecoOf := map[string]string{}
	for _, d := range found {
		all = append(all, d.pkg)
		ecoOf[d.pkg.Name] = d.ecoID
	}
	return all, ecoOf, nil
}

// reconcileOverlays drops base-ecosystem packages that an overlay ecosystem has
// claimed by directory. For every discovered package whose ecosystem declares
// Overlays, the (baseID, dir) pairs it covers are recorded; any package from one
// of those base ecosystems sharing the directory is then removed and the overlay
// survives, so the unit is released once.
//
// Before dropping a base package, its intra-repo dependency edges are handed to
// the claiming overlay (when the overlay computed none of its own). The overlay
// is the same unit the base discovered — same dir, same name — so those edges are
// genuinely its dependencies; transferring them keeps the version cascade intact
// (a workspace-lib bump still patch-bumps the desktop app the overlay owns).
// Adapters without overlay relationships leave the set untouched.
func (w *Workspace) reconcileOverlays(found []discovered) []discovered {
	type claim struct{ baseID, dir string }
	// Map each claimed (baseID, dir) to the index of the overlay package claiming
	// it, so a dropped base can pass its dependency edges to that overlay.
	claimedBy := map[claim]int{}
	for i, d := range found {
		eco, ok := w.Registry.Get(d.ecoID)
		if !ok {
			continue
		}
		for _, baseID := range eco.Info().Overlays {
			claimedBy[claim{baseID: baseID, dir: d.pkg.Dir}] = i
		}
	}
	if len(claimedBy) == 0 {
		return found
	}

	// Transfer dependency edges from each claimed base to its overlay first
	// (separate pass, so the result is independent of registration order).
	for _, d := range found {
		if oi, taken := claimedBy[claim{baseID: d.ecoID, dir: d.pkg.Dir}]; taken {
			if len(found[oi].pkg.Dependencies) == 0 {
				found[oi].pkg.Dependencies = d.pkg.Dependencies
			}
		}
	}

	kept := make([]discovered, 0, len(found))
	for _, d := range found {
		if _, taken := claimedBy[claim{baseID: d.ecoID, dir: d.pkg.Dir}]; taken {
			continue // a base package an overlay took over
		}
		kept = append(kept, d)
	}
	return kept
}

// EcosystemFor returns the adapter with the given id.
func (w *Workspace) EcosystemFor(ecoID string) (plugin.Ecosystem, bool) {
	return w.Registry.Get(ecoID)
}
