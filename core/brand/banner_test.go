package brand

import (
	"strings"
	"testing"
)

// Banners run through lipgloss, which disables color when stdout isn't a TTY
// (as in tests), so these assertions match on the plain rendered text.

func TestBannerContent(t *testing.T) {
	cases := []struct {
		name    string
		fn      func(string) string
		glyph   string
		word    string // wordmark as it reads once the muted prefix + bold stem join
		tagline string
	}{
		{"rig", RigBanner, "●", "rig", "convention-first dev launcher"},
		{"change", ChangeBanner, "↻", "changeRig", "changeset lifecycle"},
		{"ship", ShipBanner, "↑", "shipRig", "release front door"},
		{"claude", ClaudeBanner, "✳", "claudeRig", "Claude Code setup sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.fn("1.4.0")
			for _, want := range []string{tc.glyph, tc.word, tc.tagline, "v1.4.0", "rigsmith.dev", "╭─╴", "╰─╴"} {
				if !strings.Contains(out, want) {
					t.Errorf("banner missing %q; got:\n%s", want, out)
				}
			}
			if got := strings.Count(out, "\n"); got != 2 {
				t.Errorf("banner should be three lines (2 newlines); got %d:\n%s", got, out)
			}
		})
	}
}

func TestBannerOmitsEmptyVersion(t *testing.T) {
	out := RigBanner("")
	if strings.Contains(out, "  v") || strings.Contains(out, "rig  ") {
		t.Errorf("empty version should add no version token; got:\n%s", out)
	}
}

func TestFormatVersion(t *testing.T) {
	cases := map[string]string{
		"1.4.0":                    "v1.4.0",
		"v1.4.0":                   "v1.4.0",
		"dev":                      "dev",
		"unknown (built from src)": "unknown (built from src)",
		"":                         "",
	}
	for in, want := range cases {
		if got := formatVersion(in); got != want {
			t.Errorf("formatVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
