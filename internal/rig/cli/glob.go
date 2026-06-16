package cli

import (
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// excluded reports whether name matches any of the .rig.json `exclude` globs.
func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

// projectExcluded reports whether a project is hidden by the `exclude` globs,
// matching on its full name, its short name, or its repo-relative path. Path
// matching lets a glob like "testdata/*" hide a whole directory of projects
// (the '*' in globMatch spans '/'), complementing plain name globs.
func projectExcluded(name, short, relPath string, patterns []string) bool {
	if excluded(name, patterns) || (short != "" && short != name && excluded(short, patterns)) {
		return true
	}
	return relPath != "" && relPath != "." && excluded(relPath, patterns)
}

// excludeFor returns the merged .rig.json `exclude` globs for root (best-effort;
// nil when there is no config). Used to keep the interactive pickers and
// cross-ecosystem discovery consistent with `info`, which already filters.
func excludeFor(root string) []string {
	cfg, _ := config.LoadMerged(root)
	return cfg.Exclude
}

// globMatch is minimal glob matching for config patterns (the .NET rig's Glob
// semantics): '*' matches any run of characters, '?' a single one.
// Case-insensitive, anchored (the whole input must match). The implementation
// lives in detect so project discovery shares it.
func globMatch(pattern, name string) bool {
	return detect.GlobMatch(pattern, name)
}
