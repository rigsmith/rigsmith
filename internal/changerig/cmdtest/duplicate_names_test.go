package cmdtest

import (
	"path/filepath"
	"testing"
)

// Changesets, the plan and every command after discovery identify a package by
// name alone, so two packages under one name are refused at discovery rather
// than silently merged or dropped.

func TestDuplicateNameAcrossEcosystemsIsRefused(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"shared": "1.0.0"})
	writeFile(t, filepath.Join(dir, "gomod", "go.mod"), "module shared\n\ngo 1.22\n")
	initChangesets(t, dir)

	code, out := runChangerig(t, dir, "status")

	assertExitNonZero(t, code, out)
	assertContains(t, out, `more than one package is named "shared"`)
	assertContains(t, out, "packages/shared/package.json")
	assertContains(t, out, "gomod/go.mod")
}

// Within one ecosystem the second package used to vanish without a word,
// because repeats were recognized by name.
func TestDuplicateNameWithinAnEcosystemIsRefused(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, nil)
	writeFile(t, filepath.Join(dir, "packages", "one", "package.json"), `{ "name": "dup", "version": "1.0.0" }`)
	writeFile(t, filepath.Join(dir, "packages", "two", "package.json"), `{ "name": "dup", "version": "2.0.0" }`)
	initChangesets(t, dir)

	code, out := runChangerig(t, dir, "status")

	assertExitNonZero(t, code, out)
	assertContains(t, out, `more than one package is named "dup"`)
	assertContains(t, out, "packages/one/package.json")
	assertContains(t, out, "packages/two/package.json")
}

// The same package reached through two overlapping roots is still one package.
func TestOverlappingRootsFindOnePackage(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "paths": [".", "packages"] }`)
	writeChangeset(t, dir, "cs", "pkg-a", "patch", "a fix")

	code, out := runChangerig(t, dir, "status")

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a")
	assertNotContains(t, out, "more than one package")
}
