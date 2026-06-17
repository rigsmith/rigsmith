package commands

import (
	"testing"

	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/semver"
)

// TestGroupByVersionFile: modules sharing a version file group together (one
// override prompt, applied to all), while inline modules stay singletons.
func TestGroupByVersionFile(t *testing.T) {
	plan := []*planner.Module{
		{Name: "a", VersionFile: "version.txt"},
		{Name: "b", VersionFile: "version.txt"}, // shares with a
		{Name: "c", ManifestPath: "c/pkg.json"}, // inline → own group
		{Name: "d", RangeOnly: true},            // skipped
	}
	groups := groupByVersionFile(plan)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(groups), groups)
	}
	if len(groups[0]) != 2 || groups[0][0].Name != "a" || groups[0][1].Name != "b" {
		t.Errorf("first group should be [a b], got %+v", groups[0])
	}
	if len(groups[1]) != 1 || groups[1][0].Name != "c" {
		t.Errorf("second group should be [c], got %+v", groups[1])
	}
}

// TestCanonicalizeOverride documents that the override stores a canonical semver
// string even though Parse accepts non-canonical input — the same normalization
// the custom-version branch applies before writing VersionOverride.
func TestCanonicalizeOverride(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.2", "1.2.0"},
		{"01.2.3", "1.2.3"},
		{"2", "2.0.0"},
	} {
		v, ok := semver.Parse(tc.in)
		if !ok {
			t.Fatalf("Parse(%q) failed", tc.in)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("canonical %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}
