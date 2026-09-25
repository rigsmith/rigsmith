package cmdtest

import "testing"

// In commit mode, a release commit and a dependency bot's chore(deps) aren't
// changes: with no tag to start from, they used to appear in the plan as
// patch lines of their own.
func TestCommitModeSkipsReleaseAndDepsCommits(t *testing.T) {
	dir := commitRepo(t, "")
	commitIn(t, dir, "lib", "a", "feat: a real feature")
	commitIn(t, dir, "lib", "b", "chore: release 1.1.0")
	commitIn(t, dir, "lib", "c", "chore(deps): update dependency left-pad to v2")
	commitIn(t, dir, "lib", "d", "chore: tidy the build")

	code, out := runChangerig(t, dir, "status", "--verbose")
	assertExitZero(t, code, out)
	assertContains(t, out, "a real feature")
	assertContains(t, out, "tidy the build")
	assertNotContains(t, out, "release 1.1.0")
	assertNotContains(t, out, "left-pad")
}
