package semver

import "testing"

func TestSatisfies(t *testing.T) {
	cases := []struct {
		version string
		rng     string
		want    bool
	}{
		// Wildcards / empty.
		{"1.2.3", "", true},
		{"0.0.0", "", true},
		{"1.2.3", "*", true},
		{"9.9.9", "*", true},
		{"1.2.3", "x", true},
		{"1.2.3", "X", true},

		// Caret, normal (major != 0).
		{"1.2.3", "^1.2.3", true},
		{"1.2.4", "^1.2.3", true},
		{"1.9.9", "^1.2.3", true},
		{"2.0.0", "^1.2.3", false},
		{"1.2.2", "^1.2.3", false},
		{"0.9.9", "^1.2.3", false},
		{"1.5.0", "^1.2", true},
		{"2.0.0", "^1.2", false},
		{"3.4.5", "^3", true},
		{"4.0.0", "^3", false},

		// Caret, 0.x (minor != 0, major == 0).
		{"0.2.3", "^0.2.3", true},
		{"0.2.9", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"0.2.2", "^0.2.3", false},

		// Caret, 0.0.x (major == minor == 0).
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		{"0.0.2", "^0.0.3", false},

		// Caret 0 partials.
		{"0.5.0", "^0", true},
		{"1.0.0", "^0", false},

		// Tilde, full.
		{"1.2.3", "~1.2.3", true},
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"1.2.2", "~1.2.3", false},

		// Tilde, partial minor.
		{"1.2.0", "~1.2", true},
		{"1.2.9", "~1.2", true},
		{"1.3.0", "~1.2", false},
		{"1.1.9", "~1.2", false},

		// Tilde, partial major.
		{"1.0.0", "~1", true},
		{"1.9.9", "~1", true},
		{"2.0.0", "~1", false},
		{"0.9.9", "~1", false},

		// Exact / bare full version.
		{"1.2.3", "1.2.3", true},
		{"1.2.4", "1.2.3", false},
		{"1.2.3", "=1.2.3", true},
		{"1.2.4", "=1.2.3", false},

		// Comparators.
		{"1.2.3", ">=1.2.3", true},
		{"2.0.0", ">=1.2.3", true},
		{"1.2.2", ">=1.2.3", false},
		{"1.2.3", ">1.2.3", false},
		{"1.2.4", ">1.2.3", true},
		{"1.2.3", "<=1.2.3", true},
		{"1.2.2", "<=1.2.3", true},
		{"1.2.4", "<=1.2.3", false},
		{"1.2.2", "<1.2.3", true},
		{"1.2.3", "<1.2.3", false},

		// Comparators on partials.
		{"1.5.0", ">=1.2", true},
		{"1.1.0", ">=1.2", false},
		{"1.9.9", "<2", true},
		{"2.0.0", "<2", false},
		{"2.0.0", ">1.2", true},
		{"1.3.0", ">1.2", true},  // 1.3.0 is above the whole 1.2.x window
		{"1.2.9", ">1.2", false}, // still inside the 1.2.x window

		// X-ranges / partials (bare).
		{"1.0.0", "1", true},
		{"1.9.9", "1", true},
		{"2.0.0", "1", false},
		{"1.2.0", "1.2", true},
		{"1.2.9", "1.2", true},
		{"1.3.0", "1.2", false},
		{"1.5.0", "1.x", true},
		{"2.0.0", "1.x", false},
		{"1.2.7", "1.2.x", true},
		{"1.3.0", "1.2.x", false},
		{"1.7.0", "1.*", true},
		{"2.0.0", "1.*", false},

		// OR (||).
		{"1.0.0", "^1.0.0 || ^2.0.0", true},
		{"2.5.0", "^1.0.0 || ^2.0.0", true},
		{"3.0.0", "^1.0.0 || ^2.0.0", false},
		{"0.9.0", "^1.0.0 || ^2.0.0", false},
		{"1.2.3", "1.2.3 || 1.2.4", true},
		{"1.2.4", "1.2.3 || 1.2.4", true},
		{"1.2.5", "1.2.3 || 1.2.4", false},

		// Space-AND.
		{"1.5.0", ">=1.2.0 <2.0.0", true},
		{"1.2.0", ">=1.2.0 <2.0.0", true},
		{"1.1.9", ">=1.2.0 <2.0.0", false},
		{"2.0.0", ">=1.2.0 <2.0.0", false},
		{"2.0.0", ">=1.2.0 <2.0.0", false},

		// workspace: prefixes.
		{"1.2.3", "workspace:*", true},
		{"9.9.9", "workspace:*", true},
		{"1.2.3", "workspace:", true},
		{"1.5.0", "workspace:^1.0.0", true},
		{"2.0.0", "workspace:^1.0.0", false},
		{"1.2.9", "workspace:~1.2.0", true},
		{"1.3.0", "workspace:~1.2.0", false},
		{"1.2.3", "workspace:1.2.3", true},
		{"1.2.4", "workspace:1.2.3", false},

		// Prerelease (simplified): ordered below stable by Compare.
		{"1.2.3-next.0", "^1.2.3", false}, // below 1.2.3 lower bound
		{"1.2.4-next.0", "^1.2.3", true},  // within window, above lower bound
		{"2.0.0-next.0", "^1.2.3", true},  // below the 2.0.0 exclusive upper bound
		{"1.2.3", "^1.2.3-next.0", true},  // stable above prerelease lower bound
	}

	for _, c := range cases {
		v := MustParse(c.version)
		got := Satisfies(v, c.rng)
		if got != c.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", c.version, c.rng, got, c.want)
		}
	}
}

func TestSatisfiesString(t *testing.T) {
	if !SatisfiesString("1.2.3", "^1.0.0") {
		t.Errorf("SatisfiesString(1.2.3, ^1.0.0) = false, want true")
	}
	if SatisfiesString("2.0.0", "^1.0.0") {
		t.Errorf("SatisfiesString(2.0.0, ^1.0.0) = true, want false")
	}
	if SatisfiesString("not-a-version", "*") {
		t.Errorf("SatisfiesString(not-a-version, *) = true, want false (unparseable)")
	}
	if SatisfiesString("", "*") {
		t.Errorf("SatisfiesString(empty, *) = true, want false (unparseable)")
	}
}
