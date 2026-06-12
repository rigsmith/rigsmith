package cli

import (
	"testing"
)

// fixture mirrors a realistic mixed-ecosystem set of discovered packages plus
// the synthetic "(root)" target. Names are intentionally varied: a slashy Go
// module path, dotted .NET-style names, and short package names.
func cdFixture() []cdTarget {
	return []cdTarget{
		{Name: "(root)", Dir: "/repo", Rel: "."},
		{Name: "github.com/me/myapp", Dir: "/repo/cmd/myapp", Rel: "cmd/myapp"},
		{Name: "Foo.Web", Dir: "/repo/src/Foo.Web", Rel: "src/Foo.Web"},
		{Name: "Foo.Web.Tests", Dir: "/repo/tests/web", Rel: "tests/web"},
		{Name: "api", Dir: "/repo/services/api", Rel: "services/api"},
		{Name: "web-ui", Dir: "/repo/apps/web", Rel: "apps/web"},
	}
}

// findByName returns the index of name in results, or -1.
func indexOf(results []cdTarget, name string) int {
	for i, t := range results {
		if t.Name == name {
			return i
		}
	}
	return -1
}

func TestRankExactNameWins(t *testing.T) {
	res := rankCdTargets(cdFixture(), "api")
	if len(res) == 0 || res[0].Name != "api" {
		t.Fatalf("expected exact name 'api' first, got %+v", res)
	}
}

func TestRankExactCaseInsensitive(t *testing.T) {
	res := rankCdTargets(cdFixture(), "FOO.WEB")
	if len(res) == 0 || res[0].Name != "Foo.Web" {
		t.Fatalf("expected case-insensitive exact 'Foo.Web' first, got %+v", res)
	}
}

func TestRankShortName(t *testing.T) {
	// "myapp" is the short name (last '/' segment) of github.com/me/myapp.
	res := rankCdTargets(cdFixture(), "myapp")
	if len(res) == 0 || res[0].Name != "github.com/me/myapp" {
		t.Fatalf("expected short-name match for 'myapp', got %+v", res)
	}
}

func TestRankPrefixBeatsSubstring(t *testing.T) {
	// "web" is an exact match for the web-ui dir basename ("web") and also a
	// prefix of "web-ui". The short name "web-ui" starts with "web" (prefix=80),
	// while "Foo.Web" only contains it (substring=60). Prefix should win.
	res := rankCdTargets(cdFixture(), "web")
	idxUI := indexOf(res, "web-ui")
	idxFoo := indexOf(res, "Foo.Web")
	if idxUI == -1 || idxFoo == -1 {
		t.Fatalf("expected both web-ui and Foo.Web to match, got %+v", res)
	}
	if idxUI > idxFoo {
		t.Fatalf("expected prefix match web-ui before substring Foo.Web, got %+v", res)
	}
}

func TestRankSubstring(t *testing.T) {
	// "oo.we" appears as a substring of "Foo.Web" / "Foo.Web.Tests" but is not a
	// prefix and not an exact match.
	res := rankCdTargets(cdFixture(), "oo.we")
	if len(res) == 0 {
		t.Fatalf("expected substring matches for 'oo.we', got none")
	}
	for _, r := range res {
		if r.Name != "Foo.Web" && r.Name != "Foo.Web.Tests" {
			t.Fatalf("unexpected substring match %q for 'oo.we'", r.Name)
		}
	}
}

func TestRankSubsequence(t *testing.T) {
	// "fwt" is a subsequence of "foo.web.tests" but not a substring of anything.
	res := rankCdTargets(cdFixture(), "fwt")
	if len(res) == 0 || res[0].Name != "Foo.Web.Tests" {
		t.Fatalf("expected subsequence match 'Foo.Web.Tests' for 'fwt', got %+v", res)
	}
}

func TestRankNameBeatsPath(t *testing.T) {
	// "api" is an exact NAME match for "api" and a path substring for
	// "services/api". The name match must rank first.
	res := rankCdTargets(cdFixture(), "api")
	if res[0].Name != "api" {
		t.Fatalf("expected name match 'api' first, got %+v", res)
	}
}

func TestRankTierOrdering(t *testing.T) {
	// Build a focused fixture exercising exact > prefix > substring >
	// subsequence on the name field alone.
	targets := []cdTarget{
		{Name: "build", Dir: "/r/a", Rel: "a"},     // exact for "build"
		{Name: "builder", Dir: "/r/b", Rel: "b"},   // prefix
		{Name: "rebuild", Dir: "/r/c", Rel: "c"},   // substring
		{Name: "b-u-i-l-d", Dir: "/r/d", Rel: "d"}, // subsequence of query in field
		{Name: "zzz", Dir: "/r/e", Rel: "e"},       // no match
	}
	res := rankCdTargets(targets, "build")
	want := []string{"build", "builder", "rebuild", "b-u-i-l-d"}
	if len(res) != len(want) {
		t.Fatalf("expected %d matches, got %d: %+v", len(want), len(res), res)
	}
	for i, w := range want {
		if res[i].Name != w {
			t.Fatalf("tier order wrong at %d: want %q got %q (%+v)", i, w, res[i].Name, res)
		}
	}
}

func TestRankNoMatch(t *testing.T) {
	res := rankCdTargets(cdFixture(), "zzzznope")
	if len(res) != 0 {
		t.Fatalf("expected no matches, got %+v", res)
	}
}

func TestRankEmptyQueryReturnsAll(t *testing.T) {
	in := cdFixture()
	res := rankCdTargets(in, "")
	if len(res) != len(in) {
		t.Fatalf("empty query should return all %d targets, got %d", len(in), len(res))
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"github.com/me/myapp": "myapp",
		"myapp":               "myapp",
		"@scope/pkg":          "pkg",
	}
	for in, want := range cases {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSubsequence(t *testing.T) {
	if !isSubsequence("aw", "apps-web") {
		t.Error("'aw' should be a subsequence of 'apps-web'")
	}
	if isSubsequence("wa", "apps-web") {
		t.Error("'wa' should not be a subsequence of 'apps-web'")
	}
	if !isSubsequence("", "anything") {
		t.Error("empty needle is always a subsequence")
	}
}

func TestFieldScoreTiers(t *testing.T) {
	if s := fieldScore("api", "api"); s != 100 {
		t.Errorf("exact = %d, want 100", s)
	}
	if s := fieldScore("apifoo", "api"); s != 80 {
		t.Errorf("prefix = %d, want 80", s)
	}
	if s := fieldScore("fooapi", "api"); s != 60 {
		t.Errorf("substring = %d, want 60", s)
	}
	if s := fieldScore("a-p-i", "api"); s != 40 {
		t.Errorf("subsequence = %d, want 40", s)
	}
	if s := fieldScore("xyz", "api"); s != 0 {
		t.Errorf("no match = %d, want 0", s)
	}
}
