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
// '/'). On overlap the longest pattern wins, so a specific exclude can carve a
// hole in a broad include.
type Rule struct {
	Pattern string
	Action  Action
}

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
		if patternCovers(r.Pattern, rel) && len(r.Pattern) > best {
			best, act = len(r.Pattern), r.Action
		}
	}
	return act
}

func patternCovers(pattern, rel string) bool {
	if strings.ContainsAny(pattern, "*?[") {
		// A glob covers rel if it matches rel or any of rel's segment-prefixes, so
		// a glob directory (projects/*/file-history) covers its whole subtree.
		segs := strings.Split(rel, "/")
		for i := 1; i <= len(segs); i++ {
			if ok, _ := path.Match(pattern, strings.Join(segs[:i], "/")); ok {
				return true
			}
		}
		return false
	}
	return rel == pattern || strings.HasPrefix(rel, pattern+"/")
}

// descend reports whether Walk should enter directory dir. It descends when some
// include lives strictly below dir (must reach it), or when dir itself resolves
// to Include (it's inside an allowed tree and not carved out). Otherwise the
// directory is pruned — this is what keeps the Desktop cache tree untouched.
func (l List) descend(dir string) bool {
	for _, r := range l.Rules {
		if r.Action == Include && !strings.ContainsAny(r.Pattern, "*?[") &&
			strings.HasPrefix(r.Pattern, dir+"/") {
			return true
		}
	}
	return l.decide(dir) == Include
}

// Walk returns the sorted, '/'-separated relative paths of every file under root
// that the list includes, pruning irrelevant/excluded directories.
func Walk(root string, l List) ([]string, error) {
	var out []string
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
		if l.Match(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
