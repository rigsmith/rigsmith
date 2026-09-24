package cmdtest

import (
	"path/filepath"
	"testing"
)

// `version --release-as`: the override prompt's answer given up front, so a CI
// run can release at an exact version.

// releaseAsRepo has pkg-a and pkg-b at 1.0.0, each with a minor changeset.
func releaseAsRepo(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	initChangesets(t, dir)
	writeChangeset(t, dir, "a-change", "pkg-a", "minor", "A change to a")
	writeChangeset(t, dir, "b-change", "pkg-b", "minor", "A change to b")
	gitInit(t, dir)
	return dir
}

func manifest(t *testing.T, dir, pkg string) string {
	t.Helper()
	return readFile(t, filepath.Join(dir, "packages", pkg, "package.json"))
}

func TestVersionReleaseAsSetsANamedPackagesVersion(t *testing.T) {
	dir := releaseAsRepo(t)
	code, out := runChangerig(t, dir, "version", "--yes", "--release-as", "pkg-b=3.0.0")
	assertExitZero(t, code, out)

	assertContains(t, manifest(t, dir, "pkg-b"), `"3.0.0"`)
	assertContains(t, readFile(t, filepath.Join(dir, "packages", "pkg-b", "CHANGELOG.md")), "## 3.0.0")
	// pkg-a keeps its computed minor.
	assertContains(t, manifest(t, dir, "pkg-a"), `"1.1.0"`)
}

func TestVersionReleaseAsBareVersionForASinglePackage(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "a-change", "pkg-a", "patch", "A fix")
	gitInit(t, dir)
	code, out := runChangerig(t, dir, "version", "--yes", "--release-as", "2.0.0")
	assertExitZero(t, code, out)
	assertContains(t, manifest(t, dir, "pkg-a"), `"2.0.0"`)
}

// The preview shows the override, so CI can check it before writing.
func TestVersionReleaseAsShowsInThePreview(t *testing.T) {
	dir := releaseAsRepo(t)
	code, out := runChangerig(t, dir, "version", "--changelog", "--release-as", "pkg-b=3.0.0")
	assertExitZero(t, code, out)
	assertContains(t, out, "## 3.0.0")                      // the changelog heading, not just the plan line
	assertContains(t, manifest(t, dir, "pkg-b"), `"1.0.0"`) // a preview writes nothing
}

func TestVersionReleaseAsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, spec, want string
	}{
		{"not releasing", "pkg-z=3.0.0", "isn't releasing"},
		{"not above current", "pkg-b=1.0.0", "greater than the current"},
		{"not semver", "pkg-b=three", "not a valid semver"},
		{"bare with two versions", "3.0.0", "name the package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := releaseAsRepo(t)
			code, out := runChangerig(t, dir, "version", "--yes", "--release-as", tc.spec)
			assertExitNonZero(t, code, out)
			assertContains(t, out, tc.want)
			assertContains(t, manifest(t, dir, "pkg-b"), `"1.0.0"`)
			if len(changesetFiles(t, dir)) != 2 {
				t.Fatal("a refused run consumed changesets")
			}
		})
	}
}

func TestVersionReleaseAsRefusedInPrereleaseMode(t *testing.T) {
	dir := releaseAsRepo(t)
	code, out := runChangerig(t, dir, "pre", "enter", "next")
	assertExitZero(t, code, out)
	code, out = runChangerig(t, dir, "version", "--yes", "--release-as", "pkg-b=3.0.0")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "normal release")
}
