package cmdtest

import (
	"path/filepath"
	"testing"
)

// #420: after a release stops at commit, `--from publish` skipped build and
// published what was never built. shiprig now records where an unfinished
// release got to and refuses a --from past it unless forced.
//
// The pipeline stands in for the real one: commit fails until a marker
// exists (the pre-commit hook in the issue), build writes `built`, and
// publish fails unless `built` exists — so a publish that skipped build is
// visible as a failure rather than as a hollow success.
func resumeRepo(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, ".changeset", "release.jsonc"), `{
  "order": ["commit", "build", "publish"],
  "steps": {
    "commit":  { "run": "test -f commit-ok" },
    "build":   { "run": "touch built" },
    "publish": { "run": "test -f built" }
  }
}`)
	writeFile(t, filepath.Join(dir, ".gitignore"), "built\ncommit-ok\n")
	gitInit(t, dir)
	return dir
}

func TestReleaseFromPastAStoppedStepIsRefused(t *testing.T) {
	dir := resumeRepo(t)

	code, out := runShiprig(t, dir, "release", "--yes")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "step 'commit' failed")

	// The "fix": the hook passes now. Resuming past commit and build is
	// refused, and nothing runs.
	writeFile(t, filepath.Join(dir, "commit-ok"), "")
	code, out = runShiprig(t, dir, "release", "--from", "publish", "--yes")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "--from publish would skip commit, build")
	assertContains(t, out, "release --from commit")
	if fileExists(filepath.Join(dir, "built")) {
		t.Error("a refused resume must not run anything")
	}

	// Resuming where it stopped runs everything that never ran, and a
	// complete run clears the record.
	code, out = runShiprig(t, dir, "release", "--from", "commit", "--yes")
	assertExitZero(t, code, out)
	code, out = runShiprig(t, dir, "release", "--from", "publish", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, out, "--from publish skips: commit, build")
}

func TestReleaseFromWithForceSkipsAnyway(t *testing.T) {
	dir := resumeRepo(t)
	code, out := runShiprig(t, dir, "release", "--yes")
	assertExitNonZero(t, code, out)

	// Forced, the resume runs publish without build: exactly the hazard, and
	// here the stand-in publish fails because nothing was built.
	code, out = runShiprig(t, dir, "release", "--from", "publish", "--force", "--yes")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "--force: skipping commit, build")
	assertContains(t, out, "step 'publish' failed")
}

func TestReleaseFromWithoutARecordedStopIsAllowed(t *testing.T) {
	dir := resumeRepo(t)
	writeFile(t, filepath.Join(dir, "built"), "")

	// No unfinished release on record: --from is a deliberate choice, allowed
	// as before, and the skipped steps are named.
	code, out := runShiprig(t, dir, "release", "--from", "publish", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, out, "--from publish skips: commit, build")
}
