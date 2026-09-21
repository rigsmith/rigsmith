package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// generatedPackages expands each ecosystem's `publishDirs` globs into packages
// to publish.
//
// These are directories a build wrote, not sources anyone checked in: rigsmith's
// own release generates 41 npm binary-wrapper packages from the archives
// GoReleaser produced, under npm/dist/. Discovery walks the working tree for
// manifests and correctly finds none of them — they do not exist until the build
// runs, and they are gone again after a clean.
//
// So they join the run here, in the publish path alone. They are deliberately
// absent from `version`: the generator stamped each package.json from the
// release it was built for, and a cascade that rewrote those would be editing a
// build output to disagree with the binary inside it.
//
// known holds the names discovery already found. A generated directory carrying
// one of those names is skipped rather than published twice — the checked-in
// package is the one the workspace means, and publishing both would race two
// uploads of the same name against the registry.
func generatedPackages(root string, cfg *config.Config, known map[string]bool) ([]plugin.Package, map[string]string, error) {
	pkgs := []plugin.Package{}
	ecoOf := map[string]string{}

	for _, eco := range publishDirEcosystems {
		globs := cfg.EcoConfig(eco).PublishDirs
		if len(globs) == 0 {
			continue
		}
		if eco != "node" {
			// Reading a manifest means knowing its shape. Only package.json is
			// implemented, and silently ignoring the key for another ecosystem
			// would look exactly like "the build produced nothing" — the one
			// state this is designed not to treat as an error.
			return nil, nil, fmt.Errorf("%s.publishDirs: not supported yet — publishDirs is honored for node today", eco)
		}
		for _, glob := range globs {
			matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
			if err != nil {
				return nil, nil, fmt.Errorf("%s.publishDirs: %q: %w", eco, glob, err)
			}
			// Deterministic order: Glob sorts, but several globs may overlap and
			// the run's output should not depend on the order they were written.
			sort.Strings(matches)
			for _, dir := range matches {
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					continue
				}
				pkg, err := readNodePackage(root, dir)
				if err != nil {
					return nil, nil, fmt.Errorf("%s.publishDirs: %w", eco, err)
				}
				if pkg == nil || known[pkg.Name] {
					continue
				}
				known[pkg.Name] = true
				pkgs = append(pkgs, *pkg)
				ecoOf[pkg.Name] = eco
			}
		}
	}
	return pkgs, ecoOf, nil
}

// publishDirEcosystems is the fixed set of ecosystem ids whose config blocks are
// read for publishDirs. A list rather than a walk over every configured block,
// so a typo in the config cannot invent an ecosystem.
var publishDirEcosystems = []string{"node", "cargo", "dotnet"}

// readNodePackage reads a generated package.json. A directory that matched a
// glob but holds no package.json is not an error — a glob like npm/dist/* also
// matches whatever else the generator left beside the packages.
func readNodePackage(root, dir string) (*plugin.Package, error) {
	manifest := filepath.Join(dir, "package.json")
	raw, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Private bool   `json:"private"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", rel(root, manifest), err)
	}
	// Name and version are what the publish is: the registry coordinate and the
	// thing already built. A manifest missing either is a generator bug, and
	// publishing it would fail at npm with a worse message than this one.
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" {
		return nil, fmt.Errorf("%s: name and version are required", rel(root, manifest))
	}
	return &plugin.Package{
		Name:         m.Name,
		Version:      m.Version,
		Dir:          rel(root, dir),
		ManifestPath: rel(root, manifest),
		Private:      m.Private,
	}, nil
}

// rel renders a path relative to the repo root, the form plugin.Package uses.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}
