// Package walkutil provides the shared, gitignore-aware directory walk used by
// every ecosystem adapter to discover manifests. Centralizing it keeps the
// adapters consistent: they all skip the same well-known noise directories
// (node_modules, build output, VCS metadata) and the same .gitignore'd paths,
// rather than each re-implementing a slightly different walk.
//
// To keep core dependency-free the .gitignore support is a small hand-rolled
// matcher (see Ignorer) covering the common 90% of patterns rather than the full
// git semantics — see Ignored for exactly what is and isn't supported.
package walkutil

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// skippedDirs are directories never descended into, regardless of .gitignore:
// dependency trees, VCS metadata, and the per-ecosystem build output directories
// (which can contain copied-in manifests). This is the union of every adapter's
// old skip set plus the common framework caches.
var skippedDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"bin":          true,
	"obj":          true,
	"target":       true,
	"vendor":       true,
	"dist":         true,
	".next":        true,
	".turbo":       true,
}

// SkippedDir reports whether a directory with the given base name is in the
// default skip set — shared so adapters that expand workspace globs themselves
// prune the same directories Walk does.
func SkippedDir(name string) bool { return skippedDirs[name] }

// Walk descends the tree rooted at root with filepath.WalkDir and invokes fn for
// every non-skipped FILE (directories are never passed to fn). A directory is
// skipped — pruned via fs.SkipDir — when its base name is in the default skip set
// or when the repo's .gitignore matches it; matching files are likewise skipped.
// A missing root is not an error (returns nil), and unreadable subtrees are
// pruned rather than aborting the whole scan.
func Walk(root string, fn func(path string, d fs.DirEntry) error) error {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	ign := LoadIgnorer(root)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable subtrees rather than aborting the whole scan.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if skippedDirs[d.Name()] || ign.Ignored(relSlash(root, path), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ign.Ignored(relSlash(root, path), false) {
			return nil
		}
		return fn(path, d)
	})
}

// relSlash returns path relative to root using '/' separators (the form the
// .gitignore matcher works in), falling back to the base name on error.
func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

// Ignorer is a small, dependency-free .gitignore matcher. It is intentionally a
// subset of git's semantics — see Ignored for the supported and unsupported
// forms. The zero value (and a nil *Ignorer) matches nothing, so callers can use
// it unconditionally.
type Ignorer struct {
	patterns []ignorePattern
}

// ignorePattern is one parsed .gitignore line.
type ignorePattern struct {
	glob     string // the pattern body, without leading '!' or anchoring '/'
	negate   bool   // leading '!' — re-includes an otherwise-ignored path
	dirOnly  bool   // trailing '/' — matches directories only
	anchored bool   // leading '/' (or an embedded '/') — matched against the full relative path rather than any segment
}

// LoadIgnorer reads the .gitignore at root (if present) and returns a matcher for
// it. A missing or unreadable file yields an empty matcher that ignores nothing.
// Only the single root-level .gitignore is read; nested .gitignore files are not
// (deliberately — see the package doc).
func LoadIgnorer(root string) *Ignorer {
	content, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return &Ignorer{}
	}
	return parseIgnore(string(content))
}

// parseIgnore parses .gitignore text into an Ignorer.
func parseIgnore(text string) *Ignorer {
	ign := &Ignorer{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		// A leading whitespace is significant only when escaped; we don't support
		// escapes, so trimming is fine for the common case.
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := ignorePattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		// A leading '/' anchors to root; an embedded '/' likewise makes the pattern
		// a full-path match rather than a bare name matched at any depth.
		if strings.HasPrefix(line, "/") {
			p.anchored = true
			line = strings.TrimPrefix(line, "/")
		} else if strings.Contains(line, "/") {
			p.anchored = true
		}
		if line == "" {
			continue
		}
		p.glob = line
		ign.patterns = append(ign.patterns, p)
	}
	return ign
}

// Ignored reports whether the path relPath (relative to the ignorer's root, using
// '/' separators) is ignored. isDir selects whether dir-only (`trailing /`)
// patterns apply.
//
// Supported forms (the common 90%):
//   - bare names (e.g. `dist`, `*.tmp`) — match at ANY depth
//   - `*` and `?` globs within a path segment (via path.Match semantics)
//   - trailing `/` — directory-only (e.g. `build/`)
//   - leading `/` — anchored to root (e.g. `/dist`)
//   - embedded `/` — anchored full-path match (e.g. `src/generated`)
//   - `!pattern` negation — re-includes a previously ignored path
//
// Intentionally NOT supported (out of the 90% we need for manifest discovery):
//   - `**` cross-directory wildcards (a `/` in the pattern still anchors it)
//   - nested per-directory .gitignore files (only the root one is read)
//   - escaped metacharacters (`\#`, trailing-space escapes)
//   - precedence subtleties beyond "last matching pattern wins"
//
// Because the last matching pattern wins, negations work as long as they appear
// after the pattern they re-include — matching git's ordering rule.
func (i *Ignorer) Ignored(relPath string, isDir bool) bool {
	if i == nil || len(i.patterns) == 0 || relPath == "" {
		return false
	}
	relPath = strings.Trim(relPath, "/")

	ignored := false
	for _, p := range i.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.matches(relPath) {
			ignored = !p.negate
		}
	}
	return ignored
}

// matches reports whether the pattern matches relPath. An anchored pattern is
// matched against the whole relative path; a bare name is matched against each
// path segment so it applies at any depth (git's behavior for slash-less rules).
func (p ignorePattern) matches(relPath string) bool {
	if p.anchored {
		return globMatch(p.glob, relPath)
	}
	for _, seg := range strings.Split(relPath, "/") {
		if globMatch(p.glob, seg) {
			return true
		}
	}
	return false
}

// globMatch applies the glob with stdlib path.Match semantics: '*' and '?' match
// within a path segment and do not cross '/', which is exactly git's per-segment
// wildcard behavior for the forms we support.
func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	if err != nil {
		// An invalid pattern matches nothing rather than aborting the walk.
		return false
	}
	return ok
}
