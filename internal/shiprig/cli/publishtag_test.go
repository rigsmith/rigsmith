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
		{"a bad prerelease tag is refused", "", false, &prestate.PreState{Mode: prestate.ModePre, Tag: "beta 1"}, "", "pre.json's tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := publishDistTag(tc.flag, tc.packDir, tc.pre)
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
