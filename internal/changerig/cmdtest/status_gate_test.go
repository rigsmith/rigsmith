package cmdtest

import (
	"path/filepath"
	"testing"
)

// The @changesets v3 status gate without --since: the ref is the base branch,
// and status fails only when a versionable package changed on this branch and
// there is no changeset.

// gateRepo is a workspace committed on main, with a feature branch checked out
// that changes pkg-a (public) and app (private, so not versionable).
func gateRepo(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "0.1.0", "private": true }`)
	initChangesets(t, dir)
	gitInit(t, dir)
	git(t, dir, "switch", "-c", "feature")
	return dir
}

func TestStatusGateFailsWhenAPackageChangedWithoutAChangeset(t *testing.T) {
	dir := gateRepo(t)
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "export {}\n")
	gitCommitAll(t, dir, "change pkg-a")

	code, out := runChangerig(t, dir, "status")
	assertExitNonZero(t, code, out)
	assertContains(t, out, `changed since "main"`)
	assertContains(t, out, "pkg-a")

	// The gate also guards --output: a plan is not written over it.
	code, out = runChangerig(t, dir, "status", "--output", filepath.Join(dir, "plan.json"))
	assertExitNonZero(t, code, out)

	// A changeset satisfies it.
	writeChangeset(t, dir, "cs", "pkg-a", "patch", "a fix")
	gitCommitAll(t, dir, "changeset")
	code, out = runChangerig(t, dir, "status")
	assertExitZero(t, code, out)
}

func TestStatusGateIgnoresPackagesThatDoNotVersion(t *testing.T) {
	dir := gateRepo(t)
	// app is private and privatePackages is unset: not versionable.
	writeFile(t, filepath.Join(dir, "packages", "app", "index.js"), "export {}\n")
	gitCommitAll(t, dir, "change app")

	code, out := runChangerig(t, dir, "status")
	assertExitZero(t, code, out)
}
