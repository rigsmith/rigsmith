// Package match provides forgiving, path-aware fuzzy ranking shared by the
// rigsmith tools (e.g. `rig cd` project selection and `<tool>-wt` worktree
// selection). Matching is tiered — exact > prefix > substring > subsequence —
// and a query that matches nothing yields no results.
package match

import (
	"sort"
	"strings"
)

// Tier scores a field against a query: exact (100) > prefix (80) > substring
// (60) > subsequence (40) > no match (0). Both arguments are lowercased, so
// callers need not normalize.
func Tier(field, query string) int {
	h := strings.ToLower(field)
	q := strings.ToLower(query)
	switch {
	case h == q:
		return 100
	case strings.HasPrefix(h, q):
		return 80
	case strings.Contains(h, q):
		return 60
	case IsSubsequence(q, h):
		return 40
	default:
		return 0
	}
}

// IsSubsequence reports whether needle appears in haystack in order (not
// necessarily contiguously). Both are compared case-insensitively.
func IsSubsequence(needle, haystack string) bool {
	n := strings.ToLower(needle)
	h := strings.ToLower(haystack)
	if n == "" {
		return true
	}
	i := 0
	for j := 0; j < len(h) && i < len(n); j++ {
		if h[j] == n[i] {
			i++
		}
	}
	return i == len(n)
}

// ShortName is the segment after the last '/' of a (possibly scoped/slashy)
// name — e.g. "myapp" from "github.com/me/myapp", "go-watch" from
// "feat/go-watch".
func ShortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Fields describes how one item should be matched. Name fields are higher
// priority than Path fields (a name match beats a path-only match on ties).
// Depth and Tie are tiebreakers among equally-scored items: larger Depth wins
// (e.g. the deepest directory), then smaller Tie wins (e.g. the shortest name).
type Fields struct {
	Name  []string
	Path  []string
	Depth int
	Tie   int
}

// Rank returns the items matching query, best first, using fieldsOf to extract
// each item's match fields. An empty query returns a copy of items unchanged.
// Pure and stable.
func Rank[T any](items []T, query string, fieldsOf func(T) Fields) []T {
	if strings.TrimSpace(query) == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}
	type scored struct {
		item   T
		best   int
		byName bool
		depth  int
		tie    int
	}
	var hits []scored
	for _, it := range items {
		f := fieldsOf(it)
		nameTier := 0
		for _, s := range f.Name {
			nameTier = max(nameTier, Tier(s, query))
		}
		pathTier := 0
		for _, s := range f.Path {
			pathTier = max(pathTier, Tier(s, query))
		}
		best := max(nameTier, pathTier)
		if best > 0 {
			hits = append(hits, scored{it, best, nameTier > 0 && nameTier >= pathTier, f.Depth, f.Tie})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.best != b.best {
			return a.best > b.best
		}
		if a.byName != b.byName {
			return a.byName // a name match beats a path-only match
		}
		if a.depth != b.depth {
			return a.depth > b.depth // deepest on ties
		}
		return a.tie < b.tie // then the closest (shortest) name
	})
	out := make([]T, len(hits))
	for i, h := range hits {
		out[i] = h.item
	}
	return out
}
