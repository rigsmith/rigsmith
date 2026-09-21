package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The release action reports what a publish shipped, as `publishedPackages` —
// the output a consumer's workflow reads to announce a release, gate a
// follow-up job, or open an issue when nothing went out.
//
// It parses shiprig's own lines, and its pattern for the package name excluded
// "@". A scoped package starts with one, so "published @rigsmith/rig@1.19.0"
// matched nothing and fell out of the report entirely. rigsmith's 41 npm
// wrappers are all scoped, so a run publishing only those would have reported
// published=false with an empty array while 41 packages went to the registry.
//
// These drive the real script with a stub publish command rather than a copy of
// its regex, because a copy is a test that cannot fail when the original drifts.

// runReleaseAction runs release.sh on the publish path — no changesets pending —
// with publishOutput as what the publish command prints, and returns the
// step outputs it wrote.
func runReleaseAction(t *testing.T, publishOutput string) map[string]string {
	t.Helper()
	script, err := filepath.Abs("../.github/actions/release/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("release.sh not found — fix this test rather than deleting it: %v", err)
	}

	work := t.TempDir()
	// The publish path is taken when no changesets are pending; the directory
	// still has to exist, as it does in any repo using changesets.
	if err := os.MkdirAll(filepath.Join(work, ".changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(work, "github_output")
	if err := os.WriteFile(outFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", script)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		// %b so the \n escapes in the sample output become real lines.
		"INPUT_PUBLISH=printf '%b' \""+publishOutput+"\"",
		"INPUT_SETUP_GIT_USER=false",
		// Set so the script does not shell out to git for the branch name.
		"GITHUB_REF_NAME=main",
		"GITHUB_OUTPUT="+outFile,
	)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release.sh failed: %v\n%s", err, combined)
	}

	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestReleaseActionReportsScopedPackages(t *testing.T) {
	out := runReleaseAction(t, `published @rigsmith/rig@1.19.0  ok\npublished @rigsmith/rig-darwin-arm64@1.19.0  ok\n`)

	if out["published"] != "true" {
		t.Errorf("published = %q, want true — scoped packages went to the registry", out["published"])
	}
	got := out["publishedPackages"]
	for _, want := range []string{`"name":"@rigsmith/rig"`, `"name":"@rigsmith/rig-darwin-arm64"`, `"version":"1.19.0"`} {
		if !strings.Contains(got, want) {
			t.Errorf("publishedPackages missing %s\ngot: %s", want, got)
		}
	}
}

// Unscoped names have to keep working: the bundle publishes as plain `rigsmith`.
func TestReleaseActionReportsUnscopedPackages(t *testing.T) {
	out := runReleaseAction(t, `published rigsmith@1.19.0  ok\n`)
	if !strings.Contains(out["publishedPackages"], `{"name":"rigsmith","version":"1.19.0"}`) {
		t.Errorf("publishedPackages = %s, want the unscoped package", out["publishedPackages"])
	}
}

// Go modules publish by their pushed tag rather than a registry push, and that
// line has its own shape.
func TestReleaseActionReportsTaggedModules(t *testing.T) {
	out := runReleaseAction(t, `tagged+pushed github.com/rigsmith/rigsmith/v1.19.0\n`)
	if !strings.Contains(out["publishedPackages"], `"name":"github.com/rigsmith/rigsmith"`) {
		t.Errorf("publishedPackages = %s, want the tagged module", out["publishedPackages"])
	}
}

// A publish that shipped nothing must say so, rather than inventing an entry
// from a line that is not a coordinate at all.
func TestReleaseActionReportsNothingWhenNothingPublished(t *testing.T) {
	out := runReleaseAction(t, `skipped @rigsmith/rig@1.19.0  already published\nnothing to do\n`)
	if out["published"] != "false" {
		t.Errorf("published = %q, want false", out["published"])
	}
	if out["publishedPackages"] != "[]" {
		t.Errorf("publishedPackages = %q, want []", out["publishedPackages"])
	}
}
