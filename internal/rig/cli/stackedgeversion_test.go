package cli

import (
	"math"
	"strings"
	"testing"
)

func TestStackEdgeComputeVersion(t *testing.T) {
	for _, tc := range []struct {
		name, r, tree, label string
		number               uint64
		want, err            string
	}{
		{name: "tree ahead", r: "1.2.3", tree: "1.3.0", number: 5, want: "1.3.0-edge.5"},
		{name: "tree equals release", r: "1.2.3", tree: "1.2.3", number: 5, want: "1.2.4-edge.5"},
		{name: "tree behind", r: "1.2.3", tree: "1.1.0", number: 5, want: "1.2.4-edge.5"},
		{name: "MinVer", r: "1.2.3", number: 5, want: "1.2.4-edge.5"},
		{name: "first public package", tree: "0.1.0", number: 5, want: "0.1.0-edge.5"},
		{name: "missing both", number: 5, err: "needs a public version"},
		{name: "prerelease ahead", r: "1.9.0", tree: "2.0.0-rc.1", number: 5, want: "2.0.0-rc.1.edge.5"},
		{name: "prerelease below same stable core", r: "2.0.0", tree: "2.0.0-rc.1", number: 5, want: "2.0.1-edge.5"},
		{name: "prerelease without R", tree: "2.0.0-rc.1", number: 5, want: "2.0.0-rc.1.edge.5"},
		{name: "prerelease R refused", r: "2.0.0-rc.1", tree: "2.0.0", number: 5, err: "stable NuGet"},
		{name: "case and custom label", r: "1.0.0", tree: "2.0.0-RC.1", label: "Preview", number: 5, want: "2.0.0-rc.1.preview.5"},
		{name: "dotted label", r: "1.0.0", label: "preview.edge", number: 1, want: "1.0.1-preview.edge.1"},
		{name: "metadata is not prerelease", r: "1.2.3+build.9", tree: "1.3.0+build.10", number: 5, want: "1.3.0-edge.5"},
		{name: "normalized equality", r: "1.2", tree: "1.2.0.0", number: 5, want: "1.2.1-edge.5"},
		{name: "revision ahead", r: "1.2.3.4", tree: "1.2.3.5", number: 5, want: "1.2.3.5-edge.5"},
		{name: "patch bump clears revision", r: "1.2.3.4", number: 5, want: "1.2.4-edge.5"},
		{name: "local zero", r: "1.0.0", number: 0, want: "1.0.1-edge.0"},
		{name: "max number", r: "1.0.0", number: math.MaxInt32, want: "1.0.1-edge.2147483647"},
		{name: "number overflow", r: "1.0.0", number: math.MaxInt32 + 1, err: "32-bit"},
		{name: "max number overflow", r: "1.0.0", number: math.MaxUint64, err: "32-bit"},
		{name: "patch overflow", r: "1.0.2147483647", tree: "1.0.2147483647", number: 1, err: "overflow"},
		{name: "no patch bump needed", r: "1.0.2147483647", tree: "2.0.0", number: 1, want: "2.0.0-edge.1"},
		{name: "R component overflow", r: "1.0.2147483648", number: 1, err: "stable NuGet"},
		{name: "T component overflow", tree: "1.0.2147483648", number: 1, err: "in-tree"},
		{name: "T numeric label overflow", tree: "1.0.0-rc.2147483648", number: 1, err: "in-tree"},
		{name: "invalid R", r: "bad", number: 1, err: "stable NuGet"},
		{name: "invalid T", r: "1.0.0", tree: "bad", number: 1, err: "in-tree"},
		{name: "invalid label", tree: "1.0.0", label: "bad label", number: 1, err: "label"},
		{name: "metadata label", tree: "1.0.0", label: "edge+build", number: 1, err: "label"},
		{name: "empty label identifier", tree: "1.0.0", label: "edge..preview", number: 1, err: "label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stackEdgeComputeVersion(tc.r, tc.tree, tc.label, tc.number)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got.Version != tc.want || got.Publishable != (tc.number != 0) {
				t.Fatalf("got %+v, %v; want %s, publishable %v", got, err, tc.want, tc.number != 0)
			}
			v, ok := stackParseNugetVersion(got.Version)
			if !ok || v.normalized() != got.Version {
				t.Fatalf("version does not round-trip: %s", got.Version)
			}
			if tc.r != "" {
				r, _ := stackParseNugetVersion(tc.r)
				if stackCompareNuget(v, r) <= 0 {
					t.Fatalf("edge %s must sort above R %s", got.Version, tc.r)
				}
			}
		})
	}
}

func TestStackEdgePrereleaseOrdering(t *testing.T) {
	for _, r := range []string{"", "1.9.0", "2.0.0"} {
		t.Run("R="+r, func(t *testing.T) {
			got, err := stackEdgeComputeVersion(r, "2.0.0-rc.1", "edge", 5)
			if err != nil {
				t.Fatal(err)
			}
			v, ok := stackParseNugetVersion(got.Version)
			if !ok || v.normalized() != got.Version {
				t.Fatalf("not a round-trippable NuGet version: %s", got.Version)
			}
			for _, tc := range []struct {
				other string
				want  int
			}{
				{"2.0.0-rc.1", 1}, {"2.0.0-rc.2", -1}, {"2.0.0", -1},
			} {
				want := tc.want
				if r == "2.0.0" {
					want = 1 // R wins, so a bumped stable base is above all 2.0.0 versions.
				}
				other, _ := stackParseNugetVersion(tc.other)
				if c := stackCompareNuget(v, other); c != want {
					t.Errorf("compare %s to %s = %d, want %d", got.Version, tc.other, c, want)
				}
			}
		})
	}
}

func TestStackEdgeCheckMonotonic(t *testing.T) {
	for _, tc := range []struct {
		name, candidate, newest, err string
	}{
		{"no feed version", "1.0.1-edge.1", "", ""},
		{"numeric ordering", "1.0.1-edge.10", "1.0.1-edge.9", ""},
		{"same number", "1.0.1-edge.9", "1.0.1-edge.9", "increase"},
		{"workflow reset", "1.0.1-edge.1", "1.0.1-edge.100", "increase"},
		{"local cannot overtake feed", "1.0.1-edge.0", "1.0.1-edge.1", "increase"},
		{"local no feed", "1.0.1-edge.0", "", ""},
		{"prerelease base", "2.0.0-rc.1.edge.10", "2.0.0-rc.1.edge.9", ""},
		{"normalized feed", "1.0.1-edge.10", "1.0.1.0-EDGE.9+metadata", ""},
		{"other label", "1.0.1-preview.10", "1.0.1-edge.9", "same base and label"},
		{"other base", "1.1.0-edge.10", "1.0.1-edge.9", "same base and label"},
		{"other RC base", "2.0.0-rc.2.edge.10", "2.0.0-rc.1.edge.9", "same base and label"},
		{"stable newest", "1.0.1-edge.10", "1.0.1", "ending"},
		{"bad candidate", "bad", "", "ending"},
		{"bad feed number", "1.0.1-edge.10", "1.0.1-edge.x", "numeric"},
		{"overflow candidate", "1.0.1-edge.2147483648", "", "ending"},
		{"overflow newest", "1.0.1-edge.10", "1.0.1-edge.2147483648", "ending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := stackEdgeCheckMonotonic(tc.candidate, tc.newest)
			if tc.err == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("error = %v, want %q", err, tc.err)
			}
		})
	}
}
