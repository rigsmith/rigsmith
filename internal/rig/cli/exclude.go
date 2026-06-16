package cli

// Helpers behind the pickers' exclude/include controls — shared by the `run`
// picker and the `rig ui` menu: deciding when to offer a whole-directory
// exclude, finding which globs hide a project, and the .rig.json writes (over
// config.AddRepoExclude/RemoveRepoExclude) with a status line for the UI.

import (
	"strings"

	"github.com/rigsmith/rigsmith/internal/rig/config"
)

// excludeDirThreshold is how many sibling projects a top-level directory must
// hold before excluding one of them also offers to exclude the whole directory.
// Tuned so a crowded fixtures dir (examples/, testdata/) prompts the bulk option
// while a handful of binaries under cmd/ stays per-project.
const excludeDirThreshold = 5

// crowdedExcludeDir returns a "<dir>/*" glob (plus the directory and how many
// projects it holds) when rel lives under a top-level directory crowded with
// excludeDirThreshold+ projects — the cue to offer excluding the whole directory
// (e.g. examples/*) instead of one project at a time. allRels is every listed
// project's repo-relative slash path. ok is false for a top-level project or an
// uncrowded directory. The '*' spans '/', so "<dir>/*" hides the whole subtree.
func crowdedExcludeDir(rel string, allRels []string) (glob, dir string, n int, ok bool) {
	dir = firstSegment(rel)
	if dir == "" || dir == "." || dir == rel { // a top-level project has no enclosing dir
		return "", "", 0, false
	}
	for _, r := range allRels {
		if firstSegment(r) == dir {
			n++
		}
	}
	if n < excludeDirThreshold {
		return "", "", 0, false
	}
	return dir + "/*", dir, n, true
}

// matchingExcludes returns the exclude globs that currently hide a project — by
// full name, short name, or repo-relative path. Re-including a project removes
// exactly these (so a project hidden by a directory glob reveals its siblings,
// which the caller surfaces in its status line).
func matchingExcludes(name, short, rel string, patterns []string) []string {
	var hit []string
	for _, p := range patterns {
		if globMatch(p, name) ||
			(short != "" && short != name && globMatch(p, short)) ||
			(rel != "" && rel != "." && globMatch(p, rel)) {
			hit = append(hit, p)
		}
	}
	return hit
}

// preciseExcludeGlob is the glob written to exclude a single project: its name
// when that is unambiguous across the listing, else its repo-relative path (so
// two same-named binaries in different dirs stay individually targetable).
func preciseExcludeGlob(name, rel string, allNames []string) string {
	count := 0
	for _, n := range allNames {
		if n == name {
			count++
		}
	}
	if count > 1 && rel != "" && rel != "." {
		return rel
	}
	return name
}

// addExclude writes glob to root's .rig.json `exclude` and returns a status line
// for the picker (ok=false leaves config untouched).
func addExclude(root, glob string) (status string, ok bool) {
	if _, w := config.AddRepoExclude(root, excludeFor(root), glob); !w {
		return "couldn't write " + config.FileName, false
	}
	return "excluded " + glob, true
}

// removeExcludes re-includes a project by dropping every exclude glob that
// matches it (a directory glob reveals its siblings). Returns a status line and
// whether anything was hiding it.
func removeExcludes(root, name, short, rel string) (status string, ok bool) {
	hits := matchingExcludes(name, short, rel, excludeFor(root))
	if len(hits) == 0 {
		return name + " isn't excluded", false
	}
	wrote := false
	for _, g := range hits {
		if _, w := config.RemoveRepoExclude(root, excludeFor(root), g); w {
			wrote = true
		}
	}
	if !wrote {
		return "couldn't write " + config.FileName, false
	}
	return "included " + name + " (removed " + strings.Join(hits, ", ") + ")", true
}
