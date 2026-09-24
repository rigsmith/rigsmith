package cmdtest

import (
	"path/filepath"
	"testing"
)

// With a release record (versioning.record), commit-sourced releases count
// each package's commits from the commit that recorded its last release, not
// from a tag.

// commitRepo is a commit-sourced npm workspace holding lib and app at 1.0.0.
// config is extra config JSON fields.
func commitRepo(t *testing.T, config string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0", "app": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "versioning": { "source": "commits"`+config+` } }`)
	gitInit(t, dir)
	return dir
}

// commitIn adds a file under packages/<pkg> and commits it with message.
func commitIn(t *testing.T, dir, pkg, file, message string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "packages", pkg, file), message+"\n")
	gitCommitAll(t, dir, message)
}

func release(t *testing.T, dir string, args ...string) {
	t.Helper()
	code, out := runChangerig(t, dir, append([]string{"version", "--yes"}, args...)...)
	assertExitZero(t, code, out)
	gitCommitAll(t, dir, "chore: release")
}

func TestRecordBaselineCountsOnlyNewCommits(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "new lib fix")
	assertContains(t, out, "1.1.0 → 1.1.1") // a patch on the recorded release
	assertNotContains(t, out, "old lib feature")
}

// Each package counts from its own last release: releasing app alone
// doesn't swallow the lib commit made before it.
func TestRecordBaselineIsPerPackage(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	commitIn(t, dir, "app", "a", "feat: old app feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: pending lib fix")
	commitIn(t, dir, "app", "b", "feat: new app feature")
	release(t, dir, "--ignore", "lib") // app's second release; lib waits

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "pending lib fix")
	assertNotContains(t, out, "old lib feature")
	assertNotContains(t, out, "new app feature") // released by the app-only run
	assertNotContains(t, out, "app ")            // nothing pending for app
}

// Without the record, the baseline is the tag, as before: an npm-style tag
// isn't one it finds, so the history is counted again.
func TestNoRecordKeepsTheTagBaseline(t *testing.T) {
	dir := commitRepo(t, "")
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "old lib feature")
}

// The record wins over a tag: an older module tag (packages/lib/v1.0.0, on
// the first commit) doesn't pull the released feature back in.
func TestRecordBaselineWinsOverAnOlderTag(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	git(t, dir, "tag", "packages/lib/v1.0.0")
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "new lib fix")
	assertNotContains(t, out, "old lib feature")
}

// Turning the record off goes back to tags, even with a record committed.
func TestRecordBaselineOffIgnoresAnExistingRecord(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "versioning": { "source": "commits" } }`)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "old lib feature")
}
