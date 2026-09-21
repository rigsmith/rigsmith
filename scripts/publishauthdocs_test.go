package scripts

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Per-ecosystem publish config is read from .changeset/config.json, keyed by
// ecosystem id — `node` for npm packages. Two documents said otherwise:
// PUBLISH-AUTH-GUIDE.md named .changeset/release.jsonc (that is the release
// PIPELINE config, a different file), and both it and site/shiprig/pipeline.md
// showed an `"npm"` block.
//
// Neither mistake announces itself. Config.EcoConfig returns the zero value for
// a block it does not recognize — no warning, no error — so someone following
// either example configured nothing at all and got the ambient credential they
// were trying to replace. Both survived until somebody implementing a feature
// against these docs put the key in the wrong file and spent a while wondering
// why the publish found no packages.
//
// So the two spellings are pinned here rather than only corrected.

var publishAuthDocs = []string{
	"../docs/PUBLISH-AUTH-GUIDE.md",
	"../site/shiprig/pipeline.md",
}

// The ecosystem ids a config block may legitimately use. `npm` is the package
// manager; `node` is the ecosystem, and the id EcoConfig is asked for.
var ecosystemBlockIDs = map[string]bool{
	"node": true, "cargo": true, "dotnet": true, "go": true, "gomod": true,
	"tauri": true, "electron": true, "python": true,
}

func TestPublishAuthDocsNameTheFileShiprigReads(t *testing.T) {
	// The filename is a comment on its own line inside the fence, so judging it
	// line by line would miss exactly the case that went wrong. Take each fenced
	// block whole: if it configures a publish, the file it names must be the one
	// shiprig reads.
	fence := regexp.MustCompile("(?s)```jsonc?\\n(.*?)```")
	for _, path := range publishAuthDocs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		blocks := fence.FindAllStringSubmatch(string(raw), -1)
		if len(blocks) == 0 {
			t.Errorf("%s: no jsonc examples found — if they moved, point this test at them "+
				"rather than deleting it", path)
			continue
		}
		checked := 0
		for _, b := range blocks {
			body := b[1]
			if !strings.Contains(body, "auth") && !strings.Contains(body, "oidc") &&
				!strings.Contains(body, "publishDirs") {
				continue
			}
			checked++
			if strings.Contains(body, "release.jsonc") {
				t.Errorf("%s: a publish-config example names .changeset/release.jsonc — that is the "+
					"release PIPELINE config. shiprig reads the per-ecosystem block from "+
					".changeset/config.json, and a block in the wrong file is silently ignored:\n%s",
					path, strings.TrimSpace(body))
			}
		}
		if checked == 0 {
			t.Errorf("%s: found no publish-config example to check", path)
		}
	}
}

func TestPublishAuthDocsUseRealEcosystemIDs(t *testing.T) {
	// A config block in a jsonc example: `"name": {` possibly followed by keys.
	block := regexp.MustCompile(`^\s*"(\w+)":\s*\{`)
	for _, path := range publishAuthDocs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		checked := 0
		for _, line := range strings.Split(string(raw), "\n") {
			m := block.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Only lines that are actually configuring a publish: the block's
			// own keys appear on the same line in these compact examples, or it
			// is one of the multi-line examples whose keys follow.
			if !strings.Contains(line, "auth") && !strings.Contains(line, "oidc") &&
				!strings.Contains(line, "publishDirs") {
				continue
			}
			checked++
			if !ecosystemBlockIDs[m[1]] {
				t.Errorf("%s: %q uses %q as a config block, which is not an ecosystem id — "+
					"npm packages are the `node` ecosystem, and a block under an unknown key is "+
					"never read and never complains", path, strings.TrimSpace(line), m[1])
			}
		}
		if checked == 0 {
			t.Errorf("%s: found no ecosystem config example to check — if the examples moved, "+
				"point this test at them rather than deleting it", path)
		}
	}
}
