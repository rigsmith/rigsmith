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
func generatedPackages(root string, cfg *config.Config, known map[string]bool) (*generated, error) {
	out := &generated{Eco: map[string]string{}, Access: map[string]string{}}
	// Two maps, because they answer different questions. `known` is what
	// discovery already owns: those names are suppressed, silently, because the
	// checked-in package is the one the workspace means. `seen` is what THIS
	// walk has generated, and a second directory claiming one of those names is
	// a conflict to report. Folding them together makes the conflict look like
	// an ordinary suppression and publishes whichever sorted first.
	seen := map[string]string{}

	for _, eco := range publishDirEcosystems {
		globs := cfg.EcoConfig(eco).PublishDirs
		if len(globs) == 0 {
			// Distinguish "no such key" from "configured and empty": EcoConfig
			// returns the zero value both when the block is absent AND when it
			// failed to decode, so a publishDirs written as a string rather than
			// a list would silently publish nothing at all.
			if declared, err := declaresPublishDirs(cfg, eco); err != nil {
				return nil, fmt.Errorf("%s.publishDirs: %w", eco, err)
			} else if declared {
				return nil, fmt.Errorf("%s.publishDirs is set but could not be read as a list of "+
					"globs — a block that fails to decode is silently ignored, which would publish "+
					"none of the generated packages and report success", eco)
			}
			continue
		}
		if eco != "node" {
			// Reading a manifest means knowing its shape. Only package.json is
			// implemented, and silently ignoring the key for another ecosystem
			// would look exactly like "the build produced nothing" — the one
			// state this is designed not to treat as an error.
			return nil, fmt.Errorf("%s.publishDirs: not supported yet — publishDirs is honored for node today", eco)
		}
		for _, glob := range globs {
			pattern := filepath.FromSlash(glob)
			// Repo-relative means repo-relative. "../elsewhere/*" or an absolute
			// path would join into a real directory outside the tree, and every
			// package built from it becomes a working directory the publisher
			// runs npm in. Checked lexically, before expansion: the directories
			// legitimately do not exist yet, so anything resolving a real path
			// would be deciding on the wrong evidence.
			if !filepath.IsLocal(pattern) {
				return nil, fmt.Errorf("%s.publishDirs: %q must be repo-relative and stay inside the repository", eco, glob)
			}
			matches, err := filepath.Glob(filepath.Join(root, pattern))
			if err != nil {
				return nil, fmt.Errorf("%s.publishDirs: %q: %w", eco, glob, err)
			}
			// Deterministic order: Glob sorts, but several globs may overlap and
			// the run's output should not depend on the order they were written.
			sort.Strings(matches)
			for _, dir := range matches {
				info, err := os.Stat(dir)
				if err != nil {
					// Not the same as "this match is not a directory". A match
					// that cannot be inspected would silently shrink the set of
					// packages published, while the run reported success.
					return nil, fmt.Errorf("%s.publishDirs: %q: %w", eco, glob, err)
				}
				if !info.IsDir() {
					continue
				}
				// A local pattern can still match a symlink whose target is
				// outside the repository; os.Stat followed the link to get here.
				inside, err := withinRepo(root, dir)
				if err != nil {
					return nil, fmt.Errorf("%s.publishDirs: %q: %w", eco, glob, err)
				}
				if !inside {
					return nil, fmt.Errorf("%s.publishDirs: %q resolves outside the repository (%s)", eco, glob, dir)
				}
				pkg, access, err := readNodePackage(root, dir)
				if err != nil {
					return nil, fmt.Errorf("%s.publishDirs: %w", eco, err)
				}
				if pkg == nil {
					continue
				}
				// A name discovery already owns: the checked-in package is what
				// the workspace means, and publishing both would race two
				// uploads of one name at the registry.
				if known[pkg.Name] {
					continue
				}
				// Two GENERATED directories claiming one name is different: a
				// glob overlapping itself is harmless, but two different
				// directories are a build that produced the same package twice,
				// and silently taking whichever sorted first would publish an
				// arbitrary one of them.
				if prev, ok := seen[pkg.Name]; ok {
					if prev != pkg.Dir {
						return nil, fmt.Errorf("%s.publishDirs: %s is generated by two directories "+
							"(%s and %s) — publishing either would be a coin toss", eco, pkg.Name, prev, pkg.Dir)
					}
					continue
				}
				seen[pkg.Name] = pkg.Dir
				out.Packages = append(out.Packages, *pkg)
				out.Eco[pkg.Name] = eco
				if access != "" {
					out.Access[pkg.Name] = access
				}
			}
		}
	}
	return out, nil
}

// generated is what the publishDirs globs produced: the packages, the ecosystem
// each belongs to, and the access any of them declares for itself.
//
// Access matters because the workspace-wide `access` is about the packages in
// the tree. These wrappers are a different population — scoped npm packages that
// must go out public, in a repo whose own config says "restricted". Publishing
// them at the workspace default would publish them privately, or fail outright.
// Each generated manifest says what it needs via npm's own publishConfig.access,
// and that wins for these packages alone.
type generated struct {
	Packages []plugin.Package
	Eco      map[string]string
	Access   map[string]string
}

// withinRepo reports whether path, with symlinks resolved, is inside root.
func withinRepo(root, path string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// publishDirEcosystems is the fixed set of ecosystem ids whose config blocks are
// read for publishDirs. A list rather than a walk over every configured block,
// so a typo in the config cannot invent an ecosystem.
var publishDirEcosystems = []string{"node", "cargo", "dotnet"}

// readNodePackage reads a generated package.json. A directory that matched a
// glob but holds no package.json is not an error — a glob like npm/dist/* also
// matches whatever else the generator left beside the packages.
func readNodePackage(root, dir string) (*plugin.Package, string, error) {
	manifest := filepath.Join(dir, "package.json")
	raw, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	// The directory was checked; the manifest inside it is a separate path and
	// can be a symlink of its own, pointing at a package.json outside the tree.
	inside, err := withinRepo(root, manifest)
	if err != nil {
		return nil, "", err
	}
	if !inside {
		return nil, "", fmt.Errorf("%s resolves outside the repository", rel(root, manifest))
	}
	var m struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		Private       bool   `json:"private"`
		PublishConfig struct {
			Access string `json:"access"`
		} `json:"publishConfig"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, "", fmt.Errorf("%s: %w", rel(root, manifest), err)
	}
	// Name and version are what the publish is: the registry coordinate and the
	// thing already built. A manifest missing either is a generator bug, and
	// publishing it would fail at npm with a worse message than this one.
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" {
		return nil, "", fmt.Errorf("%s: name and version are required", rel(root, manifest))
	}
	// npm understands two values here. Anything else — a typo like "pubic", or
	// a value from some other registry's vocabulary — is mapped to "restricted"
	// by the adapter, so a scoped package meant to be public would publish
	// privately and report success. Refuse it where the manifest can be named.
	switch access := m.PublishConfig.Access; access {
	case "", "public", "restricted":
	default:
		return nil, "", fmt.Errorf("%s: publishConfig.access is %q; npm accepts \"public\" or \"restricted\"",
			rel(root, manifest), access)
	}
	return &plugin.Package{
		Name:         m.Name,
		Version:      m.Version,
		Dir:          rel(root, dir),
		ManifestPath: rel(root, manifest),
		Private:      m.Private,
	}, m.PublishConfig.Access, nil
}

// rel renders a path relative to the repo root, the form plugin.Package uses.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

// declaresPublishDirs reports whether an ecosystem block sets publishDirs at
// all, regardless of whether it decoded. EcoConfig degrades a malformed block
// to defaults on purpose — a bad block should not fail an unrelated run — but
// that makes "absent" and "unreadable" look identical to the caller, and for
// this key the difference is between publishing nothing deliberately and
// publishing nothing by accident.
func declaresPublishDirs(cfg *config.Config, eco string) (bool, error) {
	var block map[string]json.RawMessage
	if _, err := cfg.Ecosystem(eco, &block); err != nil {
		return false, err
	}
	raw, ok := block["publishDirs"]
	if !ok {
		return false, nil
	}
	var globs []string
	if err := json.Unmarshal(raw, &globs); err != nil {
		return true, nil // present, and not a list of globs
	}
	return false, nil
}
