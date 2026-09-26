package cmdtest

import (
	"path/filepath"
	"testing"
)

// preMajorRepo has lib at 0.3.0 (with app depending on ^0.3.0), stable at
// 1.2.0, and a major changeset on each of lib and stable. config is extra
// config JSON fields.
func preMajorRepo(t *testing.T, config string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "0.3.0", "stable": "1.2.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^0.3.0" } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch"`+config+` }`)
	writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
	writeChangeset(t, dir, "stable-change", "stable", "major", "A breaking stable change")
	gitInit(t, dir)
	return dir
}

func versionOf(t *testing.T, dir, pkg string) string {
	t.Helper()
	return readFile(t, filepath.Join(dir, "packages", pkg, "package.json"))
}

// With bumpMinorPreMajor, a major below 1.0.0 is a minor; at or above 1.0.0
// it's a major as ever; and the dependent still follows its dependency out of
// its ^0.3.0.
func TestBumpMinorPreMajor(t *testing.T) {
	dir := preMajorRepo(t, `, "versioning": { "bumpMinorPreMajor": true }`)
	code, out := runChangerig(t, dir, "status")
	assertExitZero(t, code, out)
	assertContains(t, out, "minor  lib  0.3.0 → 0.4.0")

	code, out = runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, versionOf(t, dir, "lib"), `"0.4.0"`)
	assertContains(t, versionOf(t, dir, "stable"), `"2.0.0"`)
	app := versionOf(t, dir, "app")
	assertContains(t, app, `"1.0.1"`)
	assertContains(t, app, `"^0.4.0"`)
}

// Off, as canon @changesets: a major on 0.x goes to 1.0.0.
func TestNoBumpMinorPreMajorGoesToOne(t *testing.T) {
	dir := preMajorRepo(t, "")
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, versionOf(t, dir, "lib"), `"1.0.0"`)
}

// A typed breaking change is the same major, so it's a minor below 1.0.0;
// and --release-as is how 1.0.0 is reached.
func TestBumpMinorPreMajorTypedAndReleaseAs(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "0.3.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "versioning": { "bumpMinorPreMajor": true } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "typed.md"), "---\ntype: feat!\n\"lib\"\n---\n\nDrop the old API\n")
	gitInit(t, dir)

	code, out := runChangerig(t, dir, "version", "--dry-run")
	assertExitZero(t, code, out)
	assertContains(t, out, "0.3.0 → 0.4.0")

	code, out = runChangerig(t, dir, "version", "--yes", "--release-as", "lib=1.0.0")
	assertExitZero(t, code, out)
	assertContains(t, versionOf(t, dir, "lib"), `"1.0.0"`)
}

// A fixed group of pre-1.0 packages moves together, by a minor.
func TestBumpMinorPreMajorInAFixedGroup(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"a": "0.3.0", "b": "0.3.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "fixed": [["a", "b"]], "versioning": { "bumpMinorPreMajor": true } }`)
	writeChangeset(t, dir, "a-change", "a", "major", "A breaking change")
	gitInit(t, dir)
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, versionOf(t, dir, "a"), `"0.4.0"`)
	assertContains(t, versionOf(t, dir, "b"), `"0.4.0"`)
}

// The 0.x release's changelog lists the major change under the bump it
// releases at, as status reports it: Minor Changes, not Major Changes. A
// package at 1.0.0 or above keeps its Major Changes.
func TestBumpMinorPreMajorChangelogHeading(t *testing.T) {
	dir := preMajorRepo(t, `, "versioning": { "bumpMinorPreMajor": true }`)
	code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog")
	assertExitZero(t, code, out)
	assertContains(t, out, "## 0.4.0\n\n### Minor Changes\n\n- A breaking lib change")
	assertContains(t, out, "## 2.0.0\n\n### Major Changes\n\n- A breaking stable change")
}

// --release-as 1.0.0 is how the option reaches 1.0.0, and that release is a
// major: its plan line and its changelog heading say so, as they do without
// the option.
func TestBumpMinorPreMajorReleaseAsOneIsAMajor(t *testing.T) {
	for name, config := range map[string]string{
		"option":    `, "versioning": { "bumpMinorPreMajor": true }`,
		"no option": "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := preMajorRepo(t, config)
			code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog", "--release-as", "lib=1.0.0")
			assertExitZero(t, code, out)
			assertContains(t, out, "major  lib  0.3.0 → 1.0.0")
			assertContains(t, out, "## 1.0.0\n\n### Major Changes\n\n- A breaking lib change")
		})
	}
}

// A typed breaking change released as a minor stays under 💥 Breaking
// Changes: typed headings name what a change is, and it is still breaking.
func TestBumpMinorPreMajorTypedKeepsBreaking(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "0.3.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "versioning": { "bumpMinorPreMajor": true } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "typed.md"), "---\ntype: feat!\n\"lib\"\n---\n\nDrop the old API\n")
	gitInit(t, dir)
	code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog")
	assertExitZero(t, code, out)
	assertContains(t, out, "## 0.4.0\n\n### 💥 Breaking Changes\n\n- Drop the old API")
}
