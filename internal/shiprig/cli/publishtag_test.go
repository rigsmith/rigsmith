package cli

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/prestate"
)

func TestPublishDistTag(t *testing.T) {
	pre := &prestate.PreState{Mode: prestate.ModePre, Tag: "next"}
	exited := &prestate.PreState{Mode: prestate.ModeExit, Tag: "next"}
	for _, tc := range []struct {
		name    string
		flag    string
		packDir bool
		pre     *prestate.PreState
		want    string
		wantErr string
	}{
		{"a normal release leaves npm's default", "", false, nil, "", ""},
		{"--tag names it", "canary", false, nil, "canary", ""},
		{"pre mode uses the prerelease tag", "", false, pre, "next", ""},
		{"after pre exit it's a normal release", "", false, exited, "", ""},
		{"--tag is refused in pre mode", "canary", false, pre, "", "pre mode"},
		{"--tag is refused from a pack dir", "canary", true, nil, "", "--from-pack-dir"},
		{"a pack dir's plan carries its own", "", true, pre, "", ""},
		{"pre mode without a tag is refused", "", false, &prestate.PreState{Mode: prestate.ModePre}, "", "no tag"},
		{"a blank --tag is refused", "  ", false, nil, "", "letters, digits"},
		{"a --tag with a space is refused", "my tag", false, nil, "", "letters, digits"},
		{"a --tag like a version is refused", "1.2", false, nil, "", "version or range"},
		{"a --tag like a v-version is refused", "v2", false, nil, "", "version or range"},
		{"a --tag like a range is refused", "1.x", false, nil, "", "version or range"},
		{"a tag with digits is fine", "next-2", false, nil, "next-2", ""},
		{"npm's other URL-safe characters are fine", "~beta", false, nil, "~beta", ""},
		{"a leading dot is fine", ".beta", false, nil, ".beta", ""},
		{"a leading underscore is fine", "_beta", false, nil, "_beta", ""},
		{"a --tag like a tilde range is refused", "~1.2", false, nil, "", "version or range"},
		{"a --tag like a caret range is refused", "^2", false, nil, "", "letters, digits"},
		{"a --tag like a comparison is refused", ">=1", false, nil, "", "letters, digits"},
		{"a --tag of * is refused", "*", false, nil, "", "version or range"},
		{"a --tag of x is refused", "x", false, nil, "", "version or range"},
		{"a bad prerelease tag is refused", "", false, &prestate.PreState{Mode: prestate.ModePre, Tag: "beta 1"}, "", "pre.json's tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := publishDistTag(tc.flag, tc.packDir, tc.pre, true)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("= %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// Without an npm package going out, the tag isn't held to npm's rules: a
// Go- or .NET-only prerelease can be tagged "x". The canon refusals stay.
func TestPublishDistTagWithoutNpm(t *testing.T) {
	pre := &prestate.PreState{Mode: prestate.ModePre, Tag: "x"}
	if got, err := publishDistTag("", false, pre, false); err != nil || got != "x" {
		t.Errorf("pre tag x without npm = %q, %v; want x", got, err)
	}
	if _, err := publishDistTag("", false, pre, true); err == nil {
		t.Error("pre tag x with npm: want npm's refusal")
	}
	if _, err := publishDistTag("canary", false, pre, false); err == nil {
		t.Error("--tag in pre mode is refused whatever the ecosystem")
	}
}
