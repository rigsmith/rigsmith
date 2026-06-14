package cmdtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// Polyglot replacement for the C# Cleanup_* interop tests: there is no
// `.net.mkd` bridge to strip, so cleanup reduces to consumed-vs-kept, with the
// Node CLI as the oracle (verified against @changesets v3.0.0-next.5).

// A changeset naming only an ignored package releases nothing, so `version`
// keeps it on disk while consuming the others.
func TestVersionKeepsIgnoredOnlyChangeset(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-b"] }`)
	writeChangeset(t, dir, "a-change", "pkg-a", "patch", "a fix")
	writeChangeset(t, dir, "b-change", "pkg-b", "patch", "an ignored fix")

	code, out := runChangerig(t, dir, "version")

	assertExitZero(t, code, out)
	if fileExists(filepath.Join(dir, ".changeset", "a-change.md")) {
		t.Error("a-change.md was consumed and should be removed")
	}
	if !fileExists(filepath.Join(dir, ".changeset", "b-change.md")) {
		t.Error("b-change.md names only an ignored package and should be kept")
	}
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.1"`) {
		t.Errorf("pkg-a should be versioned to 1.0.1:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-b", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("pkg-b is ignored and should stay 1.0.0:\n%s", got)
	}
}

// A changeset mixing ignored and not-ignored packages fails the run before
// anything is written (Node: "Mixed changesets ... are not allowed").
func TestVersionMixedIgnoredChangesetFails(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-b"] }`)
	writeFile(t, filepath.Join(dir, ".changeset", "ab.md"),
		"---\n\"pkg-a\": patch\n\"pkg-b\": patch\n---\n\nboth packages\n")

	code, out := runChangerig(t, dir, "version")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "mixes ignored")
	if !fileExists(filepath.Join(dir, ".changeset", "ab.md")) {
		t.Error("failed run must not consume the changeset")
	}
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("failed run must not version pkg-a:\n%s", got)
	}
}

// A changeset naming a package that isn't in the workspace fails the run
// (Node errors too); nothing is versioned or removed.
func TestVersionUnknownPackageChangesetFails(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "a-change", "pkg-a", "patch", "a fix")
	writeChangeset(t, dir, "ghost", "pkg-ghost", "patch", "a typo")

	code, out := runChangerig(t, dir, "version")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "not in the workspace")
	for _, f := range []string{"a-change.md", "ghost.md"} {
		if !fileExists(filepath.Join(dir, ".changeset", f)) {
			t.Errorf("failed run must not consume %s", f)
		}
	}
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("failed run must not version pkg-a:\n%s", got)
	}
}
