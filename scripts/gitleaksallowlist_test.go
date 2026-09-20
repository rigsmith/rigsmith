package scripts

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The gitleaks allowlist is load-bearing and silent when it is wrong: a path
// pattern that matches nothing suppresses nothing, and nothing says so. That
// has happened twice — the pattern named `clauderig/internal/redact/…`, was
// repointed to `internal/clauderig/…`, and went stale again when the package
// moved to `internal/agentrig/`. The second time it took CI down for every PR
// in the repo, because `gitleaks git` scans the whole object graph, so
// fixtures on one branch fail the scan on all of them.
//
// A dead pattern is therefore the thing to test for. It cannot be caught by
// running gitleaks — a scan with a dead allowlist entry passes perfectly well
// until someone adds a fixture it was supposed to cover.

const gitleaksConfig = "../.gitleaks.toml"

// The closing bracket has to be the one at the start of a line. A lazy `.*?]`
// stops at the first `]` in the block, which is the one inside `[^/]` — and the
// block then holds no complete TOML literal, so nothing is checked.
var pathsBlockRe = regexp.MustCompile(`(?s)paths = \[(.*?)\n\]`)
var tomlLiteralRe = regexp.MustCompile(`'''(.*?)'''`)

// allowlistPathPatterns pulls every `paths` entry out of the config.
func allowlistPathPatterns(t *testing.T) []string {
	t.Helper()
	// The working tree, not HEAD: this asserts the config you are about to
	// commit, which is the only version anyone can still fix.
	raw := readFileOrFail(t, gitleaksConfig)
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	var out []string
	for _, block := range pathsBlockRe.FindAllStringSubmatch(text, -1) {
		for _, m := range tomlLiteralRe.FindAllStringSubmatch(block[1], -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// everyPathEverCommitted lists every path in the whole object graph, which is
// the set gitleaks itself scans.
func everyPathEverCommitted(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--objects", "--all")
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git rev-list unavailable: %v", err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		_, p, ok := strings.Cut(line, " ")
		if !ok || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths
}

func TestEveryAllowlistPathStillMatchesSomething(t *testing.T) {
	patterns := allowlistPathPatterns(t)
	if len(patterns) == 0 {
		t.Fatal("no path patterns parsed out of .gitleaks.toml — fix this test's " +
			"pattern rather than deleting it, or it stops checking anything")
	}
	paths := everyPathEverCommitted(t)
	if len(paths) == 0 {
		t.Fatal("no paths found in history — this test would pass vacuously")
	}

	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Errorf("allowlist path %q does not compile as a regex: %v", p, err)
			continue
		}
		matched := false
		for _, path := range paths {
			if re.MatchString(path) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("allowlist path %q matches nothing in the repository's history, "+
				"so it suppresses nothing. Either the code moved and the pattern needs the "+
				"new location adding (keep the old one — history is still scanned), or the "+
				"entry is dead and should go.", p)
		}
	}
}

// Anchoring is not cosmetic: gitleaks matches `paths` as an unanchored regex, so
// an unanchored package pattern also allows a copy under testdata/ and, without
// a trailing anchor, a `.go.bak` beside the real file. Both were verified to
// slip through before the anchors went on.
func TestPackagePathAllowlistsAreAnchored(t *testing.T) {
	for _, p := range allowlistPathPatterns(t) {
		if !strings.HasPrefix(p, "^") || !strings.HasSuffix(p, "$") {
			t.Errorf("allowlist path %q is not anchored at both ends; gitleaks matches "+
				"these as a substring regex, so it would also allow paths that merely "+
				"contain it — a copy under testdata/, or a .go.bak beside the file", p)
		}
	}
}

func readFileOrFail(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
