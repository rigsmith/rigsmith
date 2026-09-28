package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scripts/winget-submit-each.sh submits each package as its own winget PR,
// retries a failed one, never opens a second PR for a package, and reports
// what never went out. These drive it against a fake komac and a fake curl.

// fakeKomac records each `submit <dir>` and fails a package's first
// $FAIL_<Name> attempts (Name is the package's last identifier segment).
const fakeKomac = `#!/bin/sh
[ "$1" = submit ] || { echo "fake komac: unexpected $*" >&2; exit 2; }
name=$(basename "$(dirname "$2")")
echo "$name" >>"$CALLS"
n=$(grep -cx "$name" "$CALLS")
eval "fail=\${FAIL_$name:-0}"
if [ "$n" -le "$fail" ]; then
  # $PR_THEN_FAIL: komac opened the PR, then failed.
  case " ${PR_THEN_FAIL:-} " in *" $name "*) echo "RigSmith.$name" >>"$OPENED" ;; esac
  echo "Error: Ref cannot be created." >&2
  exit 1
fi
exit 0
`

// fakeCurl answers the three GitHub calls pr_open makes. A package listed in
// $OPEN, or written to $OPENED by fakeKomac, has a komac branch with an open PR.
const fakeCurl = `#!/bin/sh
for arg in "$@"; do url=$arg; done
case "$url" in
  */user) printf '{\n  "login": "someone",\n  "id": 1\n}\n' ;;
  */git/matching-refs/heads/*)
    prefix=${url##*/heads/}
    for id in ${OPEN:-} $(cat "$OPENED" 2>/dev/null); do
      case "$prefix" in
        "$id"-*) printf '[\n  {\n    "ref": "refs/heads/%sabc",\n    "object": {}\n  }\n]\n' "$prefix"; exit 0 ;;
      esac
    done
    echo '[]' ;;
  *"/pulls?"*) printf '[\n  {\n    "number": 7\n  }\n]\n' ;;
  *) echo "fake curl: unexpected $url" >&2; exit 22 ;;
esac
`

// wingetOut lays out komac's output for the given packages at 1.2.3.
func wingetOut(t *testing.T, names ...string) string {
	t.Helper()
	out := t.TempDir()
	for _, name := range names {
		dir := filepath.Join(out, "manifests", "r", "RigSmith", name, "1.2.3")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "PackageIdentifier: RigSmith." + name + "\r\nPackageVersion: 1.2.3\r\n"
		if err := os.WriteFile(filepath.Join(dir, "RigSmith."+name+".installer.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

type submitRun struct {
	out, summary string
	calls        []string
	code         int
}

func runSubmitEach(t *testing.T, out string, env ...string) submitRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"komac": fakeKomac, "curl": fakeCurl} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(t.TempDir(), "calls")
	summary := filepath.Join(t.TempDir(), "summary.md")
	cmd := exec.Command("sh", "winget-submit-each.sh", out)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+calls,
		"OPENED="+filepath.Join(t.TempDir(), "opened"),
		"GITHUB_TOKEN=fake",
		"GITHUB_STEP_SUMMARY="+summary,
		"WINGET_SUBMIT_WAIT=0",
	)
	cmd.Env = append(cmd.Env, env...)
	b, err := cmd.CombinedOutput()
	r := submitRun{out: string(b)}
	if e, ok := err.(*exec.ExitError); ok {
		r.code = e.ExitCode()
	} else if err != nil {
		t.Fatalf("running winget-submit-each.sh: %v\n%s", err, b)
	}
	if c, err := os.ReadFile(calls); err == nil {
		r.calls = strings.Fields(string(c))
	}
	if s, err := os.ReadFile(summary); err == nil {
		r.summary = string(s)
	}
	return r
}

func TestWingetSubmitsEachPackageOnce(t *testing.T) {
	r := runSubmitEach(t, wingetOut(t, "Rig", "ShipRig"))
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	if got := strings.Join(r.calls, " "); got != "Rig ShipRig" {
		t.Errorf("submitted %q, want each package once", got)
	}
	if r.summary != "" {
		t.Errorf("a clean run wrote to the summary:\n%s", r.summary)
	}
}

// 1.23.0: the first package's submission failed and took the rest with it.
func TestWingetRetriesAFailureAndStillSubmitsTheRest(t *testing.T) {
	r := runSubmitEach(t, wingetOut(t, "Rig", "ShipRig"), "FAIL_Rig=1")
	if r.code != 0 {
		t.Fatalf("exit %d after a retry succeeded:\n%s", r.code, r.out)
	}
	if got := strings.Join(r.calls, " "); got != "Rig Rig ShipRig" {
		t.Errorf("submitted %q, want Rig retried once and ShipRig still submitted", got)
	}
	if !strings.Contains(r.out, "::warning::RigSmith.Rig 1.2.3: submission failed (attempt 1 of 3)") {
		t.Errorf("the retry wasn't announced:\n%s", r.out)
	}
}

func TestWingetReportsAPackageThatNeverWentOut(t *testing.T) {
	r := runSubmitEach(t, wingetOut(t, "Rig", "ShipRig"), "FAIL_Rig=9", "WINGET_SUBMIT_TRIES=2")
	if r.code == 0 {
		t.Fatalf("exit 0 with RigSmith.Rig never submitted:\n%s", r.out)
	}
	if got := strings.Join(r.calls, " "); got != "Rig Rig ShipRig" {
		t.Errorf("submitted %q, want two tries at Rig and ShipRig still submitted", got)
	}
	if !strings.Contains(r.out, "::error::RigSmith.Rig was not submitted to winget after 2 attempts") ||
		strings.Contains(r.out, "::error::RigSmith.ShipRig") {
		t.Errorf("want an error for Rig alone:\n%s", r.out)
	}
	if !strings.Contains(r.summary, "RigSmith.Rig") || !strings.Contains(r.summary, "winget-submit.sh 1.2.3 --submit") {
		t.Errorf("the summary doesn't name the package and the resubmit command:\n%s", r.summary)
	}
}

// Re-running after a partial failure skips what already went out.
func TestWingetSkipsAPackageWhosePRIsOpen(t *testing.T) {
	r := runSubmitEach(t, wingetOut(t, "Rig", "ShipRig"), "OPEN=RigSmith.Rig")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	if got := strings.Join(r.calls, " "); got != "ShipRig" {
		t.Errorf("submitted %q, want only ShipRig: Rig's PR is already open", got)
	}
}

// komac can fail after opening the PR; retrying then would open a second one.
func TestWingetDoesNotRetryWhenThePRWentOut(t *testing.T) {
	r := runSubmitEach(t, wingetOut(t, "Rig"), "FAIL_Rig=1", "PR_THEN_FAIL=Rig")
	if r.code != 0 {
		t.Fatalf("exit %d with Rig's PR open:\n%s", r.code, r.out)
	}
	if got := strings.Join(r.calls, " "); got != "Rig" {
		t.Errorf("submitted %q, want no retry once the PR was open", got)
	}
}
