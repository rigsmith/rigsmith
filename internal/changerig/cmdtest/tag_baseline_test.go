package cmdtest

import (
	"path/filepath"
	"testing"
)

// Without a release record, commit-sourced releases count each package's
// commits from its latest release tag, named as the tag step names it.

// statusAfterTag commits an old feature to lib, tags it (tag), then commits a
// new fix, and returns `status --verbose`.
func statusAfterTag(t *testing.T, dir, tag string) string {
	t.Helper()
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	git(t, dir, "tag", tag)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")
	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	return out
}

// The regression: a `name@version` tag used to bound nothing, so the
// released feature came back in the next plan.
func TestTagBaselineFindsNameAtVersionTags(t *testing.T) {
	out := statusAfterTag(t, commitRepo(t, ""), "lib@1.0.0")
	assertContains(t, out, "new lib fix")
	assertNotContains(t, out, "old lib feature")
}

// The highest version wins, not the newest tag or the first listed.
func TestTagBaselineTakesTheHighestVersion(t *testing.T) {
	dir := commitRepo(t, "")
	git(t, dir, "tag", "lib@0.9.0")
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	git(t, dir, "tag", "lib@1.0.0")
	git(t, dir, "tag", "lib@not-a-version")
	commitIn(t, dir, "lib", "b", "fix: new lib fix")
	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertNotContains(t, out, "old lib feature")
}

// A tagTemplate without ${name} is shared: every package counts from it.
func TestTagBaselineFollowsTheTagTemplate(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0", "app": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "versioning": { "source": "commits" }, "tagTemplate": "v${version}" }`)
	gitInit(t, dir)
	out := statusAfterTag(t, dir, "v1.0.0")
	assertContains(t, out, "new lib fix")
	assertNotContains(t, out, "old lib feature")
}

// A Go module keeps its module-path tags (dir/vX.Y.Z).
func TestTagBaselineKeepsGoModuleTags(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"app": "1.0.0"})
	writeFile(t, filepath.Join(dir, "mods", "core", "go.mod"), "module example.com/core\n\ngo 1.22\n")
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "versioning": { "source": "commits" } }`)
	gitInit(t, dir)
	writeFile(t, filepath.Join(dir, "mods", "core", "a.go"), "package core\n")
	gitCommitAll(t, dir, "feat: old core feature")
	git(t, dir, "tag", "mods/core/v1.0.0")
	writeFile(t, filepath.Join(dir, "mods", "core", "b.go"), "package core\n")
	gitCommitAll(t, dir, "fix: new core fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "new core fix")
	assertNotContains(t, out, "old core feature")
}

// A higher tag on another branch isn't this branch's release: a main fix
// made before that branch forked is still pending here, though the other
// branch's tag has it in its history.
func TestTagBaselineIgnoresTagsOnOtherBranches(t *testing.T) {
	dir := commitRepo(t, "")
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	git(t, dir, "tag", "lib@1.0.0")
	commitIn(t, dir, "lib", "p", "fix: pending main fix")
	git(t, dir, "checkout", "-q", "-b", "other")
	commitIn(t, dir, "lib", "o", "feat: other-branch feature")
	git(t, dir, "tag", "lib@9.0.0")
	git(t, dir, "checkout", "-q", "main")
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "pending main fix")
	assertContains(t, out, "new lib fix")
	assertNotContains(t, out, "old lib feature")
	assertNotContains(t, out, "other-branch feature")
}
