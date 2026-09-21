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

// publishExamples returns every fenced jsonc example in path that configures a
// publish — one that mentions auth, oidc or publishDirs.
func publishExamples(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	// Windows checks these files out CRLF, so the fence reads "```jsonc\r\n" and
	// a \n-anchored pattern matches nothing at all — a test that quietly checks
	// nothing rather than one that fails. (The same CRLF trap is recorded in
	// docs/WINGET-SUBMISSIONS.md for manifest parsing.) It was caught here only
	// because "no examples found" is itself a failure.
	fence := regexp.MustCompile("(?s)```jsonc?\\n(.*?)```")
	var out []string
	for _, m := range fence.FindAllStringSubmatch(normalizeEOL(raw), -1) {
		b := m[1]
		if strings.Contains(b, "auth") || strings.Contains(b, "oidc") || strings.Contains(b, "publishDirs") {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: no publish-config examples found — if they moved, point this test at them "+
			"rather than deleting it", path)
	}
	return out
}

// Every publish-config example must name the file shiprig actually reads.
// Rejecting only "release.jsonc" would let any other wrong filename through,
// so this requires the right one to be present rather than one wrong one to be
// absent.
func TestPublishAuthDocsNameTheFileShiprigReads(t *testing.T) {
	for _, path := range publishAuthDocs {
		for _, block := range publishExamples(t, path) {
			if !strings.Contains(block, ".changeset/config.json") {
				t.Errorf("%s: a publish-config example does not name .changeset/config.json — "+
					"that is the file shiprig reads the per-ecosystem block from, and a block in "+
					"any other file is silently ignored:\n%s", path, strings.TrimSpace(block))
			}
		}
	}
}

// Every config block inside those examples must be keyed by an ecosystem id.
// Compact examples put the block and its keys on one line; the publishDirs
// example spans several — and only checking the compact shape meant the very
// example this PR added went unchecked.
func TestPublishAuthDocsUseRealEcosystemIDs(t *testing.T) {
	block := regexp.MustCompile(`(?m)^\s*"(\w+)"\s*:\s*\{`)
	for _, path := range publishAuthDocs {
		for _, example := range publishExamples(t, path) {
			keys := block.FindAllStringSubmatch(example, -1)
			if len(keys) == 0 {
				t.Errorf("%s: a publish-config example declares no config block:\n%s",
					path, strings.TrimSpace(example))
				continue
			}
			for _, k := range keys {
				if !ecosystemBlockIDs[k[1]] {
					t.Errorf("%s: %q is not an ecosystem id — npm packages are the `node` "+
						"ecosystem, and a block under an unknown key is never read and never "+
						"complains:\n%s", path, k[1], strings.TrimSpace(example))
				}
			}
		}
	}
}

// normalizeEOL renders a file's bytes with LF endings, so patterns written
// against \n match on a Windows checkout too.
func normalizeEOL(raw []byte) string {
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
