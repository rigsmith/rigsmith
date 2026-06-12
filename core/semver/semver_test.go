package semver

import "testing"

func TestParseRoundTrip(t *testing.T) {
	cases := []string{"1.2.3", "0.0.0", "1.2.3-next.0", "1.2.3-rc.1+build.5", "10.20.30"}
	for _, c := range cases {
		v, ok := Parse(c)
		if !ok {
			t.Fatalf("Parse(%q) failed", c)
		}
		if v.String() != c {
			t.Errorf("round-trip %q -> %q", c, v.String())
		}
	}
}

func TestParseShortForms(t *testing.T) {
	v, ok := Parse("1.2")
	if !ok || v.Major != 1 || v.Minor != 2 || v.Patch != 0 {
		t.Fatalf("Parse(1.2) = %+v ok=%v", v, ok)
	}
}

func TestParseInvalid(t *testing.T) {
	for _, c := range []string{"", "  ", "a.b.c", "1.2.3-", "1.2.3-01", "-1.0.0", "1.2.3+"} {
		if _, ok := Parse(c); ok {
			t.Errorf("Parse(%q) unexpectedly ok", c)
		}
	}
}

func TestRaiseMajor(t *testing.T) {
	cases := map[string]string{
		"1.2.3":        "2.0.0",
		"1.0.0-next.0": "1.0.0", // prerelease at .0.0 graduates without incrementing
		"1.2.0-next.0": "2.0.0", // minor non-zero -> increments
		"0.0.0":        "1.0.0",
	}
	for in, want := range cases {
		got := MustParse(in).RaiseMajor().String()
		if got != want {
			t.Errorf("RaiseMajor(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestRaiseMinor(t *testing.T) {
	cases := map[string]string{
		"1.2.3":        "1.3.0",
		"1.1.0-next.0": "1.1.0", // patch zero graduates
		"1.1.5-next.0": "1.2.0", // patch non-zero increments
	}
	for in, want := range cases {
		got := MustParse(in).RaiseMinor().String()
		if got != want {
			t.Errorf("RaiseMinor(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestRaisePatch(t *testing.T) {
	cases := map[string]string{
		"1.2.3":        "1.2.4",
		"1.1.0-next.0": "1.1.0", // prerelease graduates to its own patch
	}
	for in, want := range cases {
		got := MustParse(in).RaisePatch().String()
		if got != want {
			t.Errorf("RaisePatch(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestWithPrerelease(t *testing.T) {
	stable := MustParse("1.1.0")
	if got := stable.WithPrerelease("next.0", "").String(); got != "1.1.0-next.0" {
		t.Errorf("WithPrerelease(next.0) = %s, want 1.1.0-next.0", got)
	}
	if stable.String() != "1.1.0" {
		t.Errorf("receiver mutated: %s, want 1.1.0", stable)
	}

	// Empty build keeps the receiver's build metadata; non-empty replaces it.
	withBuild := MustParse("1.1.0+build.5")
	if got := withBuild.WithPrerelease("next.0", "").String(); got != "1.1.0-next.0+build.5" {
		t.Errorf("WithPrerelease keeps build = %s, want 1.1.0-next.0+build.5", got)
	}
	if got := withBuild.WithPrerelease("next.0", "build.6").String(); got != "1.1.0-next.0+build.6" {
		t.Errorf("WithPrerelease sets build = %s, want 1.1.0-next.0+build.6", got)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0-next.0", "1.0.0", -1}, // prerelease < stable
		{"1.0.0", "1.0.0-next.0", 1},
		{"1.0.0-next.1", "1.0.0-next.2", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1}, // fewer identifiers sort lower
		{"1.0.0-1", "1.0.0-alpha", -1},       // numeric < alphanumeric
		{"1.0.0+build", "1.0.0", 0},          // build metadata ignored
	}
	for _, c := range cases {
		got := Compare(MustParse(c.a), MustParse(c.b))
		if got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPrereleaseNumber(t *testing.T) {
	if n := MustParse("1.0.0-next.3").PrereleaseNumber(); n != 3 {
		t.Errorf("PrereleaseNumber = %d, want 3", n)
	}
	if n := MustParse("1.0.0").PrereleaseNumber(); n != -1 {
		t.Errorf("PrereleaseNumber(stable) = %d, want -1", n)
	}
}
