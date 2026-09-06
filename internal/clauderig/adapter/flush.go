package adapter

import (
	"path/filepath"
	"strings"
)

// NewFlushScope resolves Options.Flush into a set keyed the way the walk will ask
// — cleaned, with symlinks resolved where the path exists — so a path the
// hook reports and the one the walk visits agree whatever the spelling.
func NewFlushScope(paths []string) FlushScope {
	var f FlushScope
	if len(paths) == 0 {
		return f
	}
	f.files = make(map[string]bool, len(paths))
	for _, p := range paths {
		c := canonicalPath(p)
		f.files[c] = true
		// A session's sub-agent transcripts live beside it, under a
		// directory of its own name: projects/<slug>/<id>/subagents/….
		// They ended with the session, and the hook names only the parent.
		if dir := strings.TrimSuffix(c, ".jsonl"); dir != c {
			f.dirs = append(f.dirs, dir+string(filepath.Separator))
		}
	}
	return f
}

// FlushScope is what a flush covers: the transcripts named, and every
// transcript under the directory a named session keeps its sub-agents in.
// Nothing else — a flush is one session's, not the machine's.
type FlushScope struct {
	files map[string]bool
	dirs  []string
}

// Covers reports whether the flush exempts path from the throttle.
func (f FlushScope) Covers(path string) bool {
	if len(f.files) == 0 {
		return false
	}
	c := canonicalPath(path)
	if f.files[c] {
		return true
	}
	for _, d := range f.dirs {
		if strings.HasPrefix(c, d) {
			return true
		}
	}
	return false
}

// canonicalPath is a path as the flush set keys it.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
