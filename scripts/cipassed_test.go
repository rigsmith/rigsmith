package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scripts/ci-passed.sh decides whether a commit is fit to release. A draft
// pull request's CI skips the macOS and Windows tests and still reports
// success, so "a run passed" isn't enough: every platform's tests have to have
// run and passed. These drive it against a fake gh with canned runs and jobs.

const fakeGh = `#!/bin/sh
# Only the two calls ci-passed.sh makes. --jq is ignored: the canned output is
# already in the shape its filter produces.
for arg in "$@"; do
  case "$arg" in
    *"/runs?head_sha="*) printf '%s\n' "$FAKE_RUNS"; exit 0 ;;
    *"/actions/runs/"*"/jobs"*)
      id=$(printf '%s' "$arg" | sed 's#.*/actions/runs/\([0-9]*\)/jobs.*#\1#')
      eval "printf '%s\n' \"\$FAKE_JOBS_$id\""
      exit 0 ;;
  esac
done
echo "fake gh: unexpected $*" >&2
exit 2
`

const allPlatforms = "test (linux)=success\ntest (macos)=success\ntest (windows)=success\nvet + gofmt=success"

func runCIPassed(t *testing.T, runs string, jobs map[string]string, args ...string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"ci-passed.sh", "abc123"}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_REPO=acme/widgets",
		"CI_WAIT_TRIES=1",
		"CI_WAIT_SECONDS=0",
		"FAKE_RUNS="+runs,
	)
	for id, js := range jobs {
		cmd.Env = append(cmd.Env, "FAKE_JOBS_"+id+"="+js)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if e, ok := err.(*exec.ExitError); ok {
		code = e.ExitCode()
	} else if err != nil {
		t.Fatalf("running ci-passed.sh: %v\n%s", err, out)
	}
	return string(out), code
}

func TestCIPassedTakesARunWithEveryPlatformsTests(t *testing.T) {
	out, code := runCIPassed(t, "11 push completed success", map[string]string{"11": allPlatforms})
	if code != 0 {
		t.Errorf("exit %d for a push run with every platform's tests passing:\n%s", code, out)
	}
}

func TestCIPassedRefusesADraftRunThatSkippedPlatforms(t *testing.T) {
	// GitHub reports the run as success with macOS and Windows skipped.
	jobs := map[string]string{"12": "test (linux)=success\ntest (macos)=skipped\ntest (windows)=skipped"}
	out, code := runCIPassed(t, "12 pull_request completed success", jobs)
	if code == 0 {
		t.Errorf("a run that skipped macOS and Windows counted as passing:\n%s", out)
	}
	if !strings.Contains(out, "test (macos)") {
		t.Errorf("the refusal doesn't name the skipped platform:\n%s", out)
	}
}

func TestCIPassedFindsAnOlderSuccessBehindNewerFailures(t *testing.T) {
	runs := "31 push completed failure\n30 pull_request completed cancelled\n29 push completed success"
	out, code := runCIPassed(t, runs, map[string]string{"29": allPlatforms})
	if code != 0 {
		t.Errorf("exit %d with a passing run behind newer failures:\n%s", code, out)
	}
}

func TestCIPassedTakesAPullRequestRunUnlessPushOnly(t *testing.T) {
	runs := "40 pull_request completed success"
	jobs := map[string]string{"40": allPlatforms}
	if out, code := runCIPassed(t, runs, jobs); code != 0 {
		t.Errorf("exit %d for a full pull request run of the exact commit:\n%s", code, out)
	}
	if out, code := runCIPassed(t, runs, jobs, "--push-only"); code == 0 {
		t.Errorf("--push-only took a pull request run:\n%s", out)
	}
}

func TestCIPassedFailsWhenNothingPassedAndNothingIsRunning(t *testing.T) {
	out, code := runCIPassed(t, "50 push completed failure", nil)
	if code == 0 || !strings.Contains(out, "none is still running") {
		t.Errorf("exit %d; want a failure saying no run passed:\n%s", code, out)
	}
}

func TestCIPassedWaitsWhileARunIsGoing(t *testing.T) {
	// One check allowed: still running when it runs out, so a failure that says
	// it hadn't passed yet, not that nothing did.
	out, code := runCIPassed(t, "60 push in_progress null", nil)
	if code == 0 || !strings.Contains(out, "hadn't passed") {
		t.Errorf("exit %d; want it to have waited on the running run:\n%s", code, out)
	}
}
