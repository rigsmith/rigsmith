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

// Initialized reports whether .changeset/config.json exists.
func (w *Workspace) Initialized() bool {
	_, err := os.Stat(filepath.Join(w.ChangesetDir, "config.json"))
	return err == nil
}

// Discover enumerates packages across every ecosystem that applies to the repo,
// returning the packages and a name→ecosystem-id map. Discovery is narrowed to
// the top-level config.Paths roots; a per-ecosystem `sourcePath` block overrides
// those for that ecosystem only. With neither, the whole repo is scanned (minus
// the usual ignores and .gitignored files).
func (w *Workspace) Discover(ctx context.Context) ([]plugin.Package, map[string]string, error) {
	var all []plugin.Package
	ecoOf := map[string]string{}
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
				all = append(all, p)
				ecoOf[p.Name] = eco.Info().ID
			}
		}
	}
	return all, ecoOf, nil
}

// EcosystemFor returns the adapter with the given id.
func (w *Workspace) EcosystemFor(ecoID string) (plugin.Ecosystem, bool) {
	return w.Registry.Get(ecoID)
}
