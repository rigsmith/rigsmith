// Package winres holds the Windows version-info resources embedded into each
// binary (see scripts/winres.sh). The test guards a trap that is invisible from
// the text itself.
package winres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// komacInstallerKeywords is BASIC_INSTALLER_KEYWORDS from komac's exe analyzer
// (src/analysis/installers/exe.rs). komac decides whether a .exe inside a zip is
// an installer or a portable binary by substring-matching these against the PE's
// FileDescription and OriginalFilename — nothing else about the binary is
// considered.
var komacInstallerKeywords = []string{"installer", "setup", "7zs.sfx", "7zsd.sfx"}

// TestDescriptionsDoNotLookLikeInstallers pins the words that decide how the
// whole Windows ecosystem classifies these binaries.
//
// clauderig's description used to read "Sync your Claude Code setup across
// machines". komac matched `setup`, wrote `NestedInstallerType: exe`, and winget
// then unpacked the zip and put nothing on PATH — which surfaced only as a
// moderator asking "Is this a Portable package?" 23 days after ClaudeRig 1.4.0
// was submitted, and again on the 1.5.1 submission. One word in a description,
// costing a release cycle each time, with nothing in the repo hinting at it.
//
// This is stricter than komac itself, which matches case-sensitively: a
// capitalised "Setup" would slip past komac today, but it reads as an installer
// to a human and to any future tightening of that check, so it fails here too.
func TestDescriptionsDoNotLookLikeInstallers(t *testing.T) {
	files, err := filepath.Glob("*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no winres configs found — this test would pass vacuously")
	}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Version map[string]map[string]struct {
				Info map[string]map[string]string `json:"info"`
			} `json:"RT_VERSION"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("%s: %v", f, err)
		}

		checked := 0
		for _, block := range cfg.Version {
			for _, lang := range block {
				for _, fields := range lang.Info {
					for _, key := range []string{"FileDescription", "OriginalFilename"} {
						value, ok := fields[key]
						if !ok {
							continue
						}
						checked++
						for _, kw := range komacInstallerKeywords {
							if strings.Contains(strings.ToLower(value), kw) {
								t.Errorf("%s: %s = %q contains %q — komac will classify this binary as an installer, "+
									"so winget will unpack the zip and put nothing on PATH. Reword it.", f, key, value, kw)
							}
						}
					}
				}
			}
		}
		if checked == 0 {
			t.Errorf("%s: found no FileDescription/OriginalFilename to check — has the config shape changed?", f)
		}
	}
}
