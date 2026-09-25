package cmdtest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildOptionsPlugin builds testdata/optionsplugin, a changelog generator that
// renders the options it receives.
func buildOptionsPlugin(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "optionsplugin")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./internal/changerig/cmdtest/testdata/optionsplugin")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the plugin: %v\n%s", err, out)
	}
	return bin
}

// A `["<plugin>", { … }]` changelog config hands the plugin its options (the
// same JSON value), as @changesets hands them to getReleaseLine: in the
// preview and in the written changelog alike.
func TestChangelogPluginReceivesItsOptions(t *testing.T) {
	plugin := buildOptionsPlugin(t)
	for _, tc := range []struct {
		name, changelog, want string
	}{
		{"tuple", `["` + filepath.ToSlash(plugin) + `", { "style": "terse", "n": 2 }]`, `options: {"style":"terse","n":2}`},
		{"string", `"` + filepath.ToSlash(plugin) + `"`, "options: none"},
		{"tuple with null options", `["` + filepath.ToSlash(plugin) + `", null]`, "options: none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newWorkspace(t)
			writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "changelog": `+tc.changelog+` }`)
			writeChangeset(t, dir, "a", "pkg-a", "minor", "A feature")
			gitInit(t, dir)

			code, out := runChangerig(t, dir, "version", "--changelog")
			assertExitZero(t, code, out)
			assertContains(t, out, tc.want)

			code, out = runChangerig(t, dir, "version", "--yes")
			assertExitZero(t, code, out)
			got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md"))
			if !strings.Contains(got, tc.want) {
				t.Errorf("CHANGELOG.md = %q, want %q", got, tc.want)
			}
		})
	}
}

// @changesets' own default, "@changesets/cli/changelog" (what `changeset init`
// writes), is the built-in layout: a module name, never a command to run.
func TestChangelogCanonDefaultModuleIsTheBuiltin(t *testing.T) {
	for _, changelog := range []string{`"@changesets/cli/changelog"`, `["@changesets/cli/changelog", null]`} {
		dir := newWorkspace(t)
		writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "changelog": `+changelog+` }`)
		writeChangeset(t, dir, "a", "pkg-a", "minor", "A feature")
		gitInit(t, dir)
		code, out := runChangerig(t, dir, "version", "--yes")
		assertExitZero(t, code, out)
		assertContains(t, readFile(t, filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md")), "### Minor Changes\n\n- A feature")
	}
}

// An external generator gets what the built-in renders from: each change's
// commit (the one that added the changeset), and the released dependencies as
// dependencyUpdates rather than as an "Updated dependencies" change.
func TestChangelogPluginReceivesRefsAndDependencies(t *testing.T) {
	plugin := buildOptionsPlugin(t)
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^1.0.0" } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "changelog": "`+filepath.ToSlash(plugin)+`" }`)
	writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
	gitInit(t, dir)
	head := git(t, dir, "rev-parse", "--short=7", "HEAD")

	code, out := runChangerig(t, dir, "version", "--changelog")
	assertExitZero(t, code, out)
	assertContains(t, out, "change: A breaking lib change commit="+head+" pr=0 author=")
	assertContains(t, out, "dependency: lib (lib) @ 2.0.0")
	assertNotContains(t, out, "change: Updated dependencies")
}

// Two released dependencies are listed in the same order, whether the entry is
// the built-in's or a plugin's: by name@version, whatever the manifest's order.
func TestChangelogDependenciesKeepTheEntrysOrder(t *testing.T) {
	plugin := buildOptionsPlugin(t)
	for _, changelog := range []string{`false`, `"` + filepath.ToSlash(plugin) + `"`} {
		dir := tempDir(t)
		writeNpmWorkspace(t, dir, map[string]string{"zeta": "1.0.0", "alpha": "1.0.0"})
		writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
			`{ "name": "app", "version": "1.0.0", "dependencies": { "zeta": "^1.0.0", "alpha": "^1.0.0" } }`)
		writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
			`{ "updateInternalDependencies": "patch", "changelog": `+changelog+` }`)
		writeChangeset(t, dir, "zeta-change", "zeta", "major", "Zeta breaks")
		writeChangeset(t, dir, "alpha-change", "alpha", "major", "Alpha breaks")
		gitInit(t, dir)
		code, out := runChangerig(t, dir, "version", "--yes")
		assertExitZero(t, code, out)
		got := readFile(t, filepath.Join(dir, "packages", "app", "CHANGELOG.md"))
		want := "- Updated dependencies\n  - alpha@2.0.0\n  - zeta@2.0.0"
		if changelog != `false` {
			want = "dependency: alpha (alpha) @ 2.0.0\ndependency: zeta (zeta) @ 2.0.0"
		}
		if !strings.Contains(got, want) {
			t.Errorf("changelog %s: CHANGELOG.md = %q, want %q", changelog, got, want)
		}
	}
}
