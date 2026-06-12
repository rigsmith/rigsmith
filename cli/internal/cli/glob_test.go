package cli

import (
	"strings"
	"testing"
)

// Ported from the .NET rig's GlobTests: '*' and '?' matching is anchored (the
// whole input must match) and case-insensitive.
func TestGlobMatch_WithStarAndQuestionAnchoredAndCaseInsensitive(t *testing.T) {
	cases := []struct {
		pattern, input string
		want           bool
	}{
		{"*Bench", "App.Bench", true},
		{"*.Demo", "Acme.Foundation.Demo", true},
		{"*Spike", "MicaSpike", true},
		{"samples/*", "samples/Foo/Foo.csproj", true},
		{"App?", "App1", true},
		{"App?", "App", false}, // ? requires exactly one char
		{"*.Demo", "Demo.App", false},
		{"Exact", "Exact", true},
		{"Exact", "Exactly", false}, // anchored: full match only
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.input); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.input, got, tc.want)
		}
		// case-insensitive
		if got := globMatch(strings.ToUpper(tc.pattern), tc.input); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", strings.ToUpper(tc.pattern), tc.input, got, tc.want)
		}
	}
}
