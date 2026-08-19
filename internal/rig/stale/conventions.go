package stale

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/walkutil"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// The generic check needs two conventions per ecosystem: what counts as source
// (so editing a README doesn't read as "you didn't rebuild") and where the
// build output lands. Both are deliberately conservative — a check that cries
// wolf gets ignored, and an ignored check is the thing that cost the evening
// this feature exists to prevent.

// sourceGlobsByEco maps an ecosystem to the globs that count as its source.
// Only files a build actually consumes: docs, fixtures and lockfile-adjacent
// noise stay out.
var sourceGlobsByEco = map[string][]string{
	detect.Go: {"**/*.go", "**/go.mod", "**/go.sum", "**/go.work"},
	detect.Node: {
		"**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs",
		"**/*.mts", "**/*.cts", "**/*.vue", "**/*.svelte",
		"**/*.css", "**/*.scss", "**/*.sass", "**/*.less", "**/*.html",
		"**/package.json", "**/tsconfig.json",
	},
	detect.DotNet: {
		"**/*.cs", "**/*.fs", "**/*.vb", "**/*.razor", "**/*.cshtml", "**/*.resx",
		"**/*.csproj", "**/*.fsproj", "**/*.vbproj",
		"**/*.props", "**/*.targets", "**/*.xaml", "**/*.axaml",
	},
	detect.Cargo: {"**/*.rs", "**/Cargo.toml", "**/Cargo.lock"},
}

// outputDirsByEco maps an ecosystem to its conventional build-output
// directories, relative to the repo root. .NET is absent: its output lives in
// per-project bin/ trees, so it is discovered (see outputDirs).
var outputDirsByEco = map[string][]string{
	detect.Go:    {"bin", "dist"},
	detect.Node:  {"dist", "build", "out", ".next", ".output", ".svelte-kit"},
	detect.Cargo: {"target/debug", "target/release"},
}

// sourceGlobs returns the source globs for an ecosystem, nil when rig has no
// convention for it (an external plugin's ecosystem, say) — the check then
// reports itself skipped rather than guessing.
func sourceGlobs(eco string) []string { return sourceGlobsByEco[eco] }

// outputNames returns the output locations an ecosystem was searched for, for
// the "nothing built yet?" message.
func outputNames(eco string) []string {
	if eco == detect.DotNet {
		return []string{"bin/<config>/<tfm>"}
	}
	if dirs := outputDirsByEco[eco]; len(dirs) > 0 {
		return dirs
	}
	return []string{"a build output directory"}
}

// outputDirs returns the build-output directories that actually EXIST under
// root for the ecosystem. Empty means nothing has been built — reported as a
// skipped check, never as a pass.
func outputDirs(root, eco string) []string {
	if eco == detect.DotNet {
		return dotnetBinDirs(root)
	}
	var out []string
	for _, rel := range outputDirsByEco[eco] {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// dotnetBinDirs finds the per-project bin/ directories under root. A .NET
// solution scatters its output across every project (bin/<config>/<tfm>/), so
// there is no single directory to stat — but the walk is cheap because a found
// bin/ is not descended into, and the usual noise is pruned.
func dotnetBinDirs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // an unreadable subtree prunes, never aborts
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if strings.EqualFold(name, "bin") {
			out = append(out, p)
			return filepath.SkipDir // the output itself — no need to descend
		}
		if walkutil.SkippedDir(name) || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		return nil
	})
	return out
}
