package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Release groups (status --output's `group`) and `version --only`: releasing
// part of a plan now and the rest later, a group at a time.

// groupsRepo has five npm packages: app depends on lib; tool and cli are
// named by one changeset; solo releases alone; extra is in the config's
// ignore list (which --only must combine with). config is extra config JSON
// fields.
func groupsRepo(t *testing.T, config string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0", "tool": "1.0.0", "cli": "1.0.0", "solo": "1.0.0", "extra": "1.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^1.0.0" } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["extra"]`+config+` }`)
	// A major on lib takes app, whose ^1.0.0 no longer covers it.
	writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
	writeFile(t, filepath.Join(dir, ".changeset", "pair.md"),
		"---\n\"tool\": minor\n\"cli\": minor\n---\n\nTool and cli together\n")
	writeChangeset(t, dir, "solo-change", "solo", "patch", "A solo fix")
	gitInit(t, dir)
	return dir
}

// statusGroups runs status --output and returns each release's group.
func statusGroups(t *testing.T, dir string) map[string]string {
	t.Helper()
	plan := filepath.Join(t.TempDir(), "plan.json")
	code, out := runChangerig(t, dir, "status", "--output", plan)
	assertExitZero(t, code, out)
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Releases []struct{ Name, Group string } `json:"releases"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("plan: %v\n%s", err, data)
	}
	groups := map[string]string{}
	for _, r := range parsed.Releases {
		groups[r.Name] = r.Group
	}
	return groups
}

func TestStatusOutputGroupsWhatMustMoveTogether(t *testing.T) {
	got := statusGroups(t, groupsRepo(t, ""))
	want := map[string]string{
		"app":  "app", // with lib: the cascade links them; "app" sorts first
		"lib":  "app",
		"cli":  "cli", // with tool: one changeset names both
		"tool": "cli",
		"solo": "solo",
	}
	for name, group := range want {
		if got[name] != group {
			t.Errorf("%s: group %q, want %q (all: %v)", name, got[name], group, got)
		}
	}
}

func TestStatusOutputGroupsAFixedGroup(t *testing.T) {
	got := statusGroups(t, groupsRepo(t, `, "fixed": [["solo", "tool"]]`))
	// solo is fixed with tool, which shares a changeset with cli.
	if got["solo"] != "cli" || got["tool"] != "cli" || got["cli"] != "cli" {
		t.Errorf("groups = %v, want solo, tool and cli together", got)
	}
}

// --only versions one group, alongside the config's ignore, and leaves the
// other groups' changesets for later.
func TestVersionOnlyReleasesOneGroup(t *testing.T) {
	dir := groupsRepo(t, "")
	code, out := runChangerig(t, dir, "version", "--yes", "--only", "tool", "--only", "cli")
	assertExitZero(t, code, out)
	// Waiting for a later run, not ignored.
	assertContains(t, out, "left 2 changeset(s) for a later run: they name only packages outside --only (lib, solo)")
	assertNotContains(t, out, "ignored packages")

	for pkg, version := range map[string]string{"tool": "1.1.0", "cli": "1.1.0", "lib": "1.0.0", "app": "1.0.0", "solo": "1.0.0"} {
		assertContains(t, readFile(t, filepath.Join(dir, "packages", pkg, "package.json")), `"`+version+`"`)
	}
	// pair.md is consumed; the others wait.
	left := map[string]bool{}
	for _, f := range changesetFiles(t, dir) {
		left[filepath.Base(f)] = true
	}
	if left["pair.md"] || !left["lib-change.md"] || !left["solo-change.md"] {
		t.Errorf("changesets left = %v, want lib-change.md and solo-change.md", left)
	}
}

// Naming part of a group is refused, pointing at the whole group.
func TestVersionOnlyRefusesPartOfAGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		only []string
	}{
		{"half a changeset", []string{"tool"}},
		{"a dependent without its dependency", []string{"app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := groupsRepo(t, "")
			args := []string{"version", "--yes"}
			for _, n := range tc.only {
				args = append(args, "--only", n)
			}
			code, out := runChangerig(t, dir, args...)
			assertExitNonZero(t, code, out)
			assertContains(t, out, "whole release group")
			if len(changesetFiles(t, dir)) != 3 {
				t.Fatal("a refused run consumed changesets")
			}
		})
	}
}

func TestVersionOnlyRefusesWithIgnore(t *testing.T) {
	dir := groupsRepo(t, "")
	code, out := runChangerig(t, dir, "version", "--yes", "--only", "solo", "--ignore", "lib")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "can't be combined")
}

// A fixed group splits with no mixed changeset and no dependency, so --only
// checks the group itself.
func TestVersionOnlyRefusesPartOfAFixedGroup(t *testing.T) {
	dir := groupsRepo(t, `, "fixed": [["solo", "tool"]]`)
	code, out := runChangerig(t, dir, "version", "--yes", "--only", "solo")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "group cli also needs cli, tool")
	if len(changesetFiles(t, dir)) != 3 {
		t.Fatal("a refused run consumed changesets")
	}
}

// status groups as version plans: an ecosystem's versionStrategy applies, so
// two .NET packages sharing Directory.Build.props but independent by their
// ecosystem block aren't one group.
func TestStatusOutputHonorsTheEcosystemStrategy(t *testing.T) {
	dir := tempDir(t)
	sharedPropsWorkspace(t, dir)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "dotnet": { "versionStrategy": "independent" } }`)
	gitInit(t, dir)
	got := statusGroups(t, dir)
	if got["A"] != "A" || got["B"] != "B" {
		t.Errorf("groups = %v, want A and B apart", got)
	}
}

// In prerelease mode, a changeset the prerelease already consumed doesn't tie
// its packages. Under @changesets v2's layout a consumed changeset stays at
// the top level, listed in pre.json; pair.md (tool with cli) is one, and
// tool's and cli's new changesets release apart.
func TestStatusOutputGroupsOnlyActiveChangesets(t *testing.T) {
	dir := groupsRepo(t, "")
	writeFile(t, filepath.Join(dir, ".changeset", "pre.json"),
		`{ "mode": "pre", "tag": "next", "initialVersions": {}, "changesets": ["pair"] }`)
	writeChangeset(t, dir, "tool-more", "tool", "patch", "More tool")
	writeChangeset(t, dir, "cli-more", "cli", "patch", "More cli")

	got := statusGroups(t, dir)
	if got["tool"] != "tool" || got["cli"] != "cli" {
		t.Errorf("groups = %v, want tool and cli apart", got)
	}
}

// A changeset the config's ignore holds back is reported as ignored, whether
// or not --only names its package; only the ones --only alone held back wait
// for a later run.
func TestVersionOnlySeparatesIgnoredFromWaiting(t *testing.T) {
	for _, only := range [][]string{{"solo"}, {"solo", "extra"}} {
		dir := groupsRepo(t, "")
		writeChangeset(t, dir, "extra-change", "extra", "patch", "An extra fix")
		args := []string{"version", "--yes"}
		for _, n := range only {
			args = append(args, "--only", n)
		}
		code, out := runChangerig(t, dir, args...)
		assertExitZero(t, code, out)
		// lib-change and pair.md wait; extra-change is ignored.
		assertContains(t, out, "left 2 changeset(s) for a later run: they name only packages outside --only (cli, lib, tool)")
		assertContains(t, out, "kept 1 changeset(s) naming only ignored packages.")
	}
}
