// Package detect resolves which ecosystem(s) a directory belongs to and maps
// rig's verbs to the right native command. Detection AND the verb→command
// mapping both come from the shared ecosystem registry in rigsmith/core, so
// rig and relrig agree on "what kind of repo is this" and an ecosystem (built-in
// or plugin) declares its own dev-loop commands.
package detect

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/core/ecosystem"
	"github.com/rigsmith/core/plugin"
)

// Ecosystem ids, matching the core adapters.
const (
	DotNet = "dotnet"
	Node   = "node"
	Go     = "go"
	Cargo  = "cargo"
)

// Root walks up from start to the repo root: the nearest dir with a .git, a
// .rig.json, or a recognized manifest. Falls back to start.
func Root(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		for _, marker := range []string{".git", ".rig.json", "go.work", "go.mod", "package.json"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
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

// ecosystemsInDir reports the distinct ecosystem ids whose manifest lives
// directly in dir (sorted, deduped). It does not recurse.
func ecosystemsInDir(dir string) []string {
	seen := map[string]bool{}
	for name, id := range manifestEcosystem {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			seen[id] = true
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(matches) > 0 {
		seen[DotNet] = true
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
