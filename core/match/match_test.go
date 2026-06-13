package match

import (
	"reflect"
	"testing"
)

func TestTier(t *testing.T) {
	cases := []struct {
		field, query string
		want         int
	}{
		{"go-watch", "go-watch", 100},  // exact
		{"Go-Watch", "go-watch", 100},  // case-insensitive exact
		{"go-watch", "go", 80},         // prefix
		{"feat/go-watch", "watch", 60}, // substring
		{"go-watch", "gwt", 40},        // subsequence
		{"go-watch", "xyz", 0},         // no match
		{"anything", "", 80},           // empty query is a prefix of everything (Rank special-cases "")
	}
	for _, c := range cases {
		if got := Tier(c.field, c.query); got != c.want {
			t.Errorf("Tier(%q,%q) = %d, want %d", c.field, c.query, got, c.want)
		}
	}
}

func TestIsSubsequence(t *testing.T) {
	if !IsSubsequence("gwt", "go-watch") {
		t.Error("gwt should be a subsequence of go-watch")
	}
	if IsSubsequence("wtg", "go-watch") {
		t.Error("wtg should not be a subsequence of go-watch")
	}
	if !IsSubsequence("", "anything") {
		t.Error("empty needle is always a subsequence")
	}
}

func TestShortName(t *testing.T) {
	if got := ShortName("feat/go-watch"); got != "go-watch" {
		t.Errorf("ShortName = %q, want go-watch", got)
	}
	if got := ShortName("main"); got != "main" {
		t.Errorf("ShortName = %q, want main", got)
	}
}

func TestRank(t *testing.T) {
	type wt struct{ branch, path string }
	items := []wt{
		{"main", "/repo"},
		{"feat/go-watch", "/repo-wt/feat-go-watch"},
		{"feat/go-watcher", "/repo-wt/feat-go-watcher"},
		{"chore/lint", "/repo-wt/chore-lint"},
	}
	fields := func(w wt) Fields {
		return Fields{
			Name:  []string{w.branch, ShortName(w.branch)},
			Path:  []string{w.path},
			Depth: len(w.path),
			Tie:   len(w.branch),
		}
	}

	// Empty query: unchanged copy.
	if got := Rank(items, "", fields); !reflect.DeepEqual(got, items) {
		t.Errorf("empty query should return items unchanged, got %v", got)
	}

	// Exact branch wins over prefix sibling.
	got := Rank(items, "feat/go-watch", fields)
	if len(got) == 0 || got[0].branch != "feat/go-watch" {
		t.Fatalf("exact match should rank first, got %v", got)
	}

	// Subsequence query narrows to the go-watch family, no false positives.
	got = Rank(items, "gowat", fields)
	for _, g := range got {
		if g.branch == "main" || g.branch == "chore/lint" {
			t.Errorf("gowat matched unrelated branch %q", g.branch)
		}
	}
	if len(got) == 0 {
		t.Error("gowat should match the go-watch branches")
	}

	// No match: empty result.
	if got := Rank(items, "zzz", fields); len(got) != 0 {
		t.Errorf("no-match query should return nothing, got %v", got)
	}
}
