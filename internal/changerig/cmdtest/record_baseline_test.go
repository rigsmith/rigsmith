package cmdtest

import (
	"encoding/json"
	"os/exec"
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

// Without the record, the baseline is the tag, as before: with none, the
// history is counted again.
func TestNoRecordKeepsTheTagBaseline(t *testing.T) {
	dir := commitRepo(t, "")
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "old lib feature")
}

// The record wins over a tag: an older release tag (lib@1.0.0, on the first
// commit) doesn't pull the released feature back in.
func TestRecordBaselineWinsOverAnOlderTag(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	git(t, dir, "tag", "lib@1.0.0")
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

// A release merged in from another branch belongs to the commit that
// recorded it there, not to the merge: app released on branch x, while main
// released lib and took an app fix. After the merge, main's app fix is still
// pending for app, since it wasn't in x's release.
func TestRecordBaselineFollowsAMergedRelease(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	commitIn(t, dir, "app", "a", "feat: old app feature")
	release(t, dir)

	git(t, dir, "checkout", "-b", "x")
	commitIn(t, dir, "app", "x", "feat: app feature on x")
	release(t, dir, "--ignore", "lib")

	git(t, dir, "checkout", "main")
	commitIn(t, dir, "app", "m", "fix: main-side app fix")
	commitIn(t, dir, "lib", "m", "feat: main lib feature")
	release(t, dir, "--ignore", "app")

	// Both sides changed the record: keep main's lib and x's app.
	mainRecord := git(t, dir, "show", "main:.changeset/versions.json")
	xRecord := git(t, dir, "show", "x:.changeset/versions.json")
	// The merge conflicts on the record; the resolution follows.
	merge := exec.Command("git", "merge", "--no-ff", "--no-commit", "x")
	merge.Dir = dir
	_ = merge.Run()
	if git(t, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD") == "" {
		t.Fatal("the merge didn't start")
	}
	writeFile(t, filepath.Join(dir, ".changeset", "versions.json"), mergeRecords(t, mainRecord, xRecord, "lib", "app"))
	git(t, dir, "checkout", "--theirs", "--", "packages/app")
	git(t, dir, "checkout", "--ours", "--", "packages/lib")
	gitCommitAll(t, dir, "merge x")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "main-side app fix")
	assertNotContains(t, out, "app feature on x")
	assertNotContains(t, out, "main lib feature")
}

// A shallow clone's cut-off history can't say which commit recorded a
// release, so the record isn't used there: with no tags, the history the
// clone has counts.
func TestRecordBaselineSkipsAShallowClone(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: old lib feature")
	release(t, dir)
	commitIn(t, dir, "lib", "b", "fix: new lib fix")

	clone := filepath.Join(tempDir(t), "clone")
	git(t, dir, "clone", "--quiet", "--depth", "1", "file://"+dir, clone)
	code, out := runChangerig(t, clone, "status", "--verbose")
	assertExitZero(t, code, out)
	// lib's own bump: the clone's one commit looks like it touches every
	// file, so app lists the fix too, whatever lib's baseline.
	assertContains(t, out, "1.1.0 → 1.1.1")
}

// mergeRecords resolves a conflicted versions.json: `released` takes ours's
// entry for each of fromOurs and theirs's for fromTheirs.
func mergeRecords(t *testing.T, ours, theirs, fromOurs, fromTheirs string) string {
	t.Helper()
	parse := func(s string) map[string]string {
		var r struct {
			Released map[string]string `json:"released"`
		}
		if err := json.Unmarshal([]byte(s), &r); err != nil {
			t.Fatalf("record: %v\n%s", err, s)
		}
		return r.Released
	}
	o, th := parse(ours), parse(theirs)
	merged := map[string]any{
		"packages": map[string]string{},
		"released": map[string]string{fromOurs: o[fromOurs], fromTheirs: th[fromTheirs]},
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// A record entry older than the package's version is stale: lib released
// again while the record was off (tagged lib@1.2.0), so the tag, not the
// stale entry, is where it counts from.
func TestRecordBaselinePassesOverAStaleEntry(t *testing.T) {
	dir := commitRepo(t, `, "record": true`)
	commitIn(t, dir, "lib", "a", "feat: first lib feature")
	release(t, dir) // recorded: lib 1.1.0

	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "versioning": { "source": "commits" } }`)
	commitIn(t, dir, "lib", "b", "feat: second lib feature")
	release(t, dir) // lib 1.2.0, unrecorded
	git(t, dir, "tag", "lib@1.2.0")

	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "versioning": { "source": "commits", "record": true } }`)
	commitIn(t, dir, "lib", "c", "fix: new lib fix")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "new lib fix")
	assertNotContains(t, out, "second lib feature")
}
