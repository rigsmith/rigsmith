package cli

import "github.com/rigsmith/cli/internal/detect"

// excluded reports whether name matches any of the .rig.json `exclude` globs.
func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

// globMatch is minimal glob matching for config patterns (the .NET rig's Glob
// semantics): '*' matches any run of characters, '?' a single one.
// Case-insensitive, anchored (the whole input must match). The implementation
// lives in detect so project discovery shares it.
func globMatch(pattern, name string) bool {
	return detect.GlobMatch(pattern, name)
}
