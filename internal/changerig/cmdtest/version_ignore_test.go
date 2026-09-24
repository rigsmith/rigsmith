package cmdtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// `version --ignore`, as @changesets has it: leave named packages out of one
// run, keeping their changesets for a later one.

// ignoreRepo is a two-package workspace with a changeset for each. depKind, when
// set, makes pkg-b depend on pkg-a under that key ("dependencies",
// "devDependencies").
func ignoreRepo(t *testing.T, depKind string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	b := `{ "name": "pkg-b", "version": "1.0.0" }`
	if depKind != "" {
		b = `{ "name": "pkg-b", "version": "1.0.0", "` + depKind + `": { "pkg-a": "^1.0.0" } }`
	}
	writeFile(t, filepath.Join(dir, "packages", "pkg-b", "package.json"), b)
	initChangesets(t, dir)
	writeChangeset(t, dir, "a-change", "pkg-a", "minor", "A change to a")
	writeChangeset(t, dir, "b-change", "pkg-b", "minor", "A change to b")
	gitInit(t, dir)
	return dir
}

func TestVersionIgnoreLeavesAPackageForLater(t *testing.T) {
	dir := ignoreRepo(t, "")

	code, out := runChangerig(t, dir, "version", "--yes", "--ignore", "pkg-b")
	assertExitZero(t, code, out)

	assertContains(t, readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")), `"1.1.0"`)
	assertContains(t, readFile(t, filepath.Join(dir, "packages", "pkg-b", "package.json")), `"1.0.0"`)
	// pkg-b's changeset waits for the next run; pkg-a's is consumed.
	files := changesetFiles(t, dir)
	if len(files) != 1 || !strings.HasSuffix(files[0], "b-change.md") {
		t.Fatalf("changesets left = %v, want only b-change.md", files)
	}
}

func TestVersionIgnoreRefusesAnUnknownName(t *testing.T) {
	dir := ignoreRepo(t, "")
	code, out := runChangerig(t, dir, "version", "--yes", "--ignore", "pkg-z")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "pkg-z")
	assertContains(t, out, "not in the workspace")
	if len(changesetFiles(t, dir)) != 2 {
		t.Fatal("a refused run consumed changesets")
	}
}

func TestVersionIgnoreRefusesAlongsideConfigIgnore(t *testing.T) {
	dir := ignoreRepo(t, "")
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-a"] }`)
	code, out := runChangerig(t, dir, "version", "--yes", "--ignore", "pkg-b")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "use one or the other")
}

// A package that depends on a skipped one must be skipped too; a dev
// dependency doesn't count.
func TestVersionRefusesToSkipAPackageItsDependentNeeds(t *testing.T) {
	dir := ignoreRepo(t, "dependencies")
	code, out := runChangerig(t, dir, "version", "--yes", "--ignore", "pkg-a")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "pkg-b depends on the skipped package pkg-a")

	dir = ignoreRepo(t, "devDependencies")
	code, out = runChangerig(t, dir, "version", "--yes", "--ignore", "pkg-a")
	assertExitZero(t, code, out)
}

// With ignore set in the config, the remedy is the config: --ignore alongside
// it is refused.
func TestSkippedDependentRemedyFollowsWhereTheIgnoreIs(t *testing.T) {
	dir := ignoreRepo(t, "dependencies")
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-a"] }`)
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "add it to `ignore` in the config too")
	assertNotContains(t, out, "pass it to --ignore")
}

func TestVersionIgnoreCompletesPackageNames(t *testing.T) {
	dir := ignoreRepo(t, "")
	code, out := runChangerig(t, dir, "__complete", "version", "--ignore", "")
	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "pkg-b")
}
