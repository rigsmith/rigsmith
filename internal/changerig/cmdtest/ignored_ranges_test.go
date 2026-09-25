package cmdtest

import (
	"path/filepath"
	"testing"
)

// An ignored package's changeset doesn't plan a release, so it doesn't move
// its dependents' ranges either: app depends on lib (^1.0.0), lib has a
// major, and both are held back. app's range must stay ^1.0.0; rewriting it
// to ^2.0.0 would name a lib version that was never released.
func TestIgnoredDependencyLeavesDependentRangesAlone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		args   []string
	}{
		{"config ignore", `, "ignore": ["lib", "app"]`, nil},
		{"--only", "", []string{"--only", "solo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0", "solo": "1.0.0"})
			writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
				`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^1.0.0" } }`)
			writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
				`{ "updateInternalDependencies": "patch"`+tc.config+` }`)
			writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
			writeChangeset(t, dir, "solo-change", "solo", "patch", "A solo fix")
			gitInit(t, dir)

			code, out := runChangerig(t, dir, append([]string{"version", "--yes"}, tc.args...)...)
			assertExitZero(t, code, out)
			assertContains(t, readFile(t, filepath.Join(dir, "packages", "solo", "package.json")), `"1.0.1"`)
			assertContains(t, readFile(t, filepath.Join(dir, "packages", "lib", "package.json")), `"1.0.0"`)
			app := readFile(t, filepath.Join(dir, "packages", "app", "package.json"))
			assertContains(t, app, `"^1.0.0"`)
			assertNotContains(t, out, "app ")
		})
	}
}

// The canon case still holds: an ignored dependent of a package that does
// release gets its range rewritten, without a release of its own.
func TestIgnoredDependentOfAReleaseStillGetsItsRange(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^1.0.0" } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["app"] }`)
	writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
	gitInit(t, dir)

	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	app := readFile(t, filepath.Join(dir, "packages", "app", "package.json"))
	assertContains(t, app, `"^2.0.0"`)
	assertContains(t, app, `"version": "1.0.0"`)
}
