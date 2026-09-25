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
