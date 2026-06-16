// Package detect resolves which ecosystem(s) a directory belongs to and maps
// rig's verbs to the right native command. Detection AND the verb→command
// mapping both come from the shared ecosystem registry in rigsmith/core, so
// rig and shiprig agree on "what kind of repo is this" and an ecosystem (built-in
// or plugin) declares its own dev-loop commands.
package detect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Ecosystem ids, matching the core adapters.
const (
	DotNet = "dotnet"
	Node   = "node"
	Go     = "go"
	Cargo  = "cargo"
)

// Root walks up from start to the repo root. Precedence is by CATEGORY, not
// distance (matching the .NET rig's RootResolver): the nearest .rig.json wins;
// else the nearest workspace manifest (*.slnx/*.sln, go.work, go.mod,
// package.json, Cargo.toml); else the nearest .git; else start. Explicit config
// therefore wins even over a closer manifest.
//
// A .git ancestor bounds the walk: it's the outer edge of the repo, so the
// search never climbs past it to anchor on a manifest / config that lives
// outside the repository (e.g. a stray solution up in the home directory).
func Root(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	var rigDir, manifestDir, gitDir string
	for d := dir; ; {
		if rigDir == "" {
			if _, err := os.Stat(filepath.Join(d, ".rig.json")); err == nil {
				rigDir = d
			}
		}
		if manifestDir == "" && hasWorkspaceManifest(d) {
			manifestDir = d
		}
		// The repo boundary: record it (inclusive of this dir's own config/
		// manifest, checked above) and stop — don't escape the repository.
		// `.git` is a directory for normal clones, a file for worktrees.
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			gitDir = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	switch {
	case rigDir != "":
		return rigDir
	case manifestDir != "":
		return manifestDir
	case gitDir != "":
		return gitDir
	}
	return dir
}

// hasWorkspaceManifest reports whether dir directly contains a workspace-level
// manifest that can anchor the repo root: a .NET solution or an ecosystem
// manifest. Per-project files (*.csproj) deliberately don't anchor the root.
func hasWorkspaceManifest(dir string) bool {
	for _, marker := range []string{"go.work", "go.mod", "package.json", "Cargo.toml"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	for _, pat := range []string{"*.slnx", "*.sln"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, pat)); len(matches) > 0 {
			return true
		}
	}
	return false
}

// manifestEcosystem maps a manifest filename — checked DIRECTLY in a directory,
// not recursively — to the ecosystem id it signals. *.csproj is handled by glob
// separately. This is the nearest-manifest resolver's source of truth and is
// deliberately independent of registration order so a polyglot repo doesn't
// silently default to whichever adapter registers first.
var manifestEcosystem = map[string]string{
	"go.mod":       Go,
	"go.work":      Go,
	"package.json": Node,
	"Cargo.toml":   Cargo,
}

// dotnetRootMarkers are repo-root files that signal a .NET repo whose solutions
// and projects live in subdirectories (Source/, src/, …) with no .sln/.csproj at
// the root itself. Each is an MSBuild / .NET-SDK / NuGet file — an unambiguous
// .NET signal. Matched case-insensitively, since .NET filenames vary in case
// across tools (NuGet.Config vs nuget.config).
var dotnetRootMarkers = map[string]bool{
	"directory.build.props":    true,
	"directory.build.targets":  true,
	"directory.packages.props": true,
	"global.json":              true,
	"nuget.config":             true,
}

// ecosystemsInDir reports the distinct ecosystem ids whose manifest lives
// directly in dir (sorted, deduped). It does not recurse.
func ecosystemsInDir(dir string) []string {
	seen := map[string]bool{}
	for name, id := range manifestEcosystem {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			seen[id] = true
		}
	}
	// A solution file is a .NET signal too (a solution-only root has no csproj
	// directly in it) — see the C# ProjectDiscovery, which anchors on solutions.
	for _, pat := range []string{"*.csproj", "*.sln", "*.slnx"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, pat)); len(matches) > 0 {
			seen[DotNet] = true
			break
		}
	}
	// …and so are the conventional repo-root markers (Directory.Build.props,
	// global.json, …) for a .NET repo whose projects all live in subdirectories.
	if !seen[DotNet] {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && dotnetRootMarkers[strings.ToLower(e.Name())] {
					seen[DotNet] = true
					break
				}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NearestEcosystem resolves the primary ecosystem by walking UP from start to
// the repo root and, at each directory, checking for manifests DIRECTLY in that
// directory (not recursively). The first (closest to start) directory that
// contains one or more manifests decides:
//
//   - exactly one ecosystem there        → (id, nil)
//   - several ecosystems at that same dir → ("", candidates) — ambiguous, don't guess
//   - none found anywhere up to the root  → ("", nil)
//
// This replaces the old order-based Primary, which always picked whichever
// adapter registered first in a polyglot repo.
func NearestEcosystem(start string) (id string, candidates []string) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	root := Root(dir)
	for {
		switch ids := ecosystemsInDir(dir); len(ids) {
		case 0:
			// keep walking up
		case 1:
			return ids[0], nil
		default:
			return "", ids
		}
		if dir == root {
			return "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// CommandFor returns the argv that runs verb in the given ecosystem id at root.
// The ecosystem is chosen by the caller (NearestEcosystem or .rig.json). For
// Node it applies package-manager detection (pnpm/yarn/bun); otherwise it uses
// the ecosystem adapter's declared DevCommands. ok=false means the id isn't
// registered or the ecosystem doesn't map the verb.
func CommandFor(eco, verb, root string) (argv []string, ok bool) {
	e, found := ecosystem.Default().Get(eco)
	if !found {
		return nil, false
	}
	if eco == Node {
		if cmd, has := nodeCommand(DetectNodePM(root), verb); has {
			return cmd, true
		}
	}
	cmd, has := e.Info().DevCommands[verb]
	return cmd, has
}

// Verbs lists the everyday dev-loop verbs rig exposes (in display order).
var Verbs = []string{plugin.VerbBuild, plugin.VerbTest, plugin.VerbRun, plugin.VerbFormat}
