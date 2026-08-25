// Package allowlist decides which files under a sync root travel to the repo. It
// is allowlist-by-default-deny — nothing syncs unless an include rule covers it —
// which is the safety property the community tools lack (a new secret-bearing file
// upstream is excluded until explicitly allowed). Directory pruning means a 12 GB
// Electron cache tree is never even descended.
package allowlist

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Action is a rule's verdict.
type Action int

const (
	Exclude Action = iota // default
	Include
)

// Rule covers paths matching Pattern (relative to the root, '/'-separated). A
// pattern with no glob metacharacter matches that path and everything under it
// (directory-prefix); a glob pattern matches per path.Match (so '*' never crosses
// '/'); a "**/name" pattern matches that segment at any depth, and everything
// under it. On overlap the most specific rule wins — measured as the number of
// path characters the rule pins down — so a specific exclude can carve a hole in
// a broad include no matter how deep the hole sits.
type Rule struct {
	Pattern string
	Action  Action
}

// anyDepth is the prefix marking a "this segment, wherever it appears" pattern.
const anyDepth = "**/"

// List is an ordered rule set, evaluated longest-pattern-wins, default deny.
type List struct {
	Rules []Rule
}

// Include/Exclude are builder helpers.
func inc(p string) Rule { return Rule{Pattern: p, Action: Include} }
func exc(p string) Rule { return Rule{Pattern: p, Action: Exclude} }

// Match reports whether a file at rel (relative to the root, '/'-separated) syncs.
func (l List) Match(rel string) bool { return l.decide(rel) == Include }

func (l List) decide(rel string) Action {
	best, act := -1, Exclude
	for _, r := range l.Rules {
		if ok, score := patternCovers(r.Pattern, rel); ok && score > best {
			best, act = score, r.Action
		}
	}
	return act
}

// patternCovers reports whether pattern covers rel, and how specific the match is
// — the count of path characters the rule pins down. For a literal or glob that's
// the pattern length; for an any-depth pattern it's the length of the matched path
// prefix, which is what lets a short "**/node_modules" outrank a long include it
// sits inside.
func patternCovers(pattern, rel string) (bool, int) {
	if name, ok := strings.CutPrefix(pattern, anyDepth); ok {
		segs := strings.Split(rel, "/")
		for i, seg := range segs {
			if m, _ := path.Match(name, seg); m {
				return true, len(strings.Join(segs[:i+1], "/"))
			}
		}
		return false, 0
	}
	if strings.ContainsAny(pattern, "*?[") {
		// A glob covers rel if it matches rel or any of rel's segment-prefixes, so
		// a glob directory (projects/*/file-history) covers its whole subtree.
		segs := strings.Split(rel, "/")
		for i := 1; i <= len(segs); i++ {
			if ok, _ := path.Match(pattern, strings.Join(segs[:i], "/")); ok {
				return true, len(pattern)
			}
		}
		return false, 0
	}
	if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
		return true, len(pattern)
	}
	return false, 0
}

// descend reports whether Walk should enter directory dir. It descends when some
// include lives strictly below dir (must reach it), or when dir itself resolves
// to Include (it's inside an allowed tree and not carved out). Otherwise the
// directory is pruned — this is what keeps the Desktop cache tree untouched.
func (l List) descend(dir string) bool {
	// An any-depth exclude is a hard prune: a tree banned by name (node_modules)
	// is never entered, whatever else the rules say about what lives under it.
	for _, r := range l.Rules {
		if r.Action != Exclude || !strings.HasPrefix(r.Pattern, anyDepth) {
			continue
		}
		if ok, _ := patternCovers(r.Pattern, dir); ok {
			return false
		}
	}
	for _, r := range l.Rules {
		if r.Action == Include && !strings.ContainsAny(r.Pattern, "*?[") &&
			strings.HasPrefix(r.Pattern, dir+"/") {
			return true
		}
	}
	return l.decide(dir) == Include
}

// Link records a symlink to a directory whose target lives inside the same root
// and is itself included — the shared-memory case: a worktree project slug links
// memory/ to the main project's memory dir. Both paths are '/'-separated and
// relative to the root. Sync records links so restore can recreate them; the
// target's content travels under its own path, never through the link.
type Link struct {
	Rel    string
	Target string
}

// Walk returns the sorted, '/'-separated relative paths of every file under root
// that the list includes, pruning irrelevant/excluded directories. A symlink to a
// directory is never returned as a file — WalkDir doesn't follow it, so it
// arrives as a non-directory entry, and emitting it would make consumers read a
// directory as a file and abort the sync. When its target resolves inside root to
// an included path it is reported as a Link; other directory links are dropped.
func Walk(root string, l List) ([]string, []Link, error) {
	var out []string
	var links []Link
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A live ~/.claude churns under us; an entry that vanished between
			// listing and visiting must not abort the walk — skip it.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if !l.descend(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if info, serr := os.Stat(p); serr == nil && info.IsDir() {
				// Report the link only when both ends are in the synced set: the
				// link path itself, and its resolved target.
				if target, ok := resolveInRoot(root, p); ok &&
					l.decide(rel) == Include && l.decide(target) == Include {
					links = append(links, Link{Rel: rel, Target: target})
				}
				// Either way this is a directory, so it is never a file to sync.
				// Falling through to Match here would offer the link path as a
				// regular file, which reading can only ever fail on (EISDIR) —
				// and staged a 0-byte placeholder that restore later tried to
				// write back over the live link.
				return nil
			}
		}
		if l.Match(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(out)
	sort.Slice(links, func(i, j int) bool { return links[i].Rel < links[j].Rel })
	return out, links, nil
}

// resolveInRoot resolves symlink p and returns its target relative to root, when
// the target lives inside root. Both sides are fully resolved first, so a root
// that itself sits behind a symlink (/var -> /private/var) compares equal.
func resolveInRoot(root, p string) (string, bool) {
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootReal, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
