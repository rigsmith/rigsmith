// Package scripts holds tests for the release shell scripts, so the checks that
// gate a release are themselves covered by `go test ./...`.
package scripts

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// runCheck runs check-winget-manifests.sh against a fixture directory.
func runCheck(t *testing.T, dir string) (out string, ok bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	cmd := exec.Command("sh", "./check-winget-manifests.sh", dir)
	b, err := cmd.CombinedOutput()
	return string(b), err == nil
}

// TestWingetCheckAcceptsRealGeneratedManifests uses the manifest GoReleaser
// actually produced for the RigSmith.Rig 1.5.0 submission, captured verbatim.
//
// The first version of this check was written against the *published* manifest
// shape, where winget's publish pipeline has flattened the keys to column 0. In
// what GoReleaser generates they are nested under `Installers:` and indented, so
// the anchored pattern matched nothing and the check failed all five correct
// manifests on its first live run — failing the release job before the npm
// publish step, which was then skipped. Hence a fixture of the real bytes.
func TestWingetCheckAcceptsRealGeneratedManifests(t *testing.T) {
	out, ok := runCheck(t, "testdata/winget-good")
	if !ok {
		t.Fatalf("real GoReleaser manifest rejected:\n%s", out)
	}
	if !strings.Contains(out, "2 installer(s), all portable") {
		t.Errorf("output = %q, want both installers counted", out)
	}
}

// TestWingetCheckCatchesAHalfBrokenManifest covers the case a spot check misses:
// one architecture portable, the other not. Broken for half its users.
func TestWingetCheckCatchesAHalfBrokenManifest(t *testing.T) {
	out, ok := runCheck(t, "testdata/winget-bad")
	if ok {
		t.Fatalf("a manifest with a non-portable installer passed:\n%s", out)
	}
	if !strings.Contains(out, "1 of 2 installer(s)") {
		t.Errorf("output = %q, want it to say how many installers are wrong", out)
	}
}

// TestWingetCheckIsQuietWithNothingToCheck: the winget pipe is publish-phase
// only, so most runs generate no manifests. That must not fail a build.
func TestWingetCheckIsQuietWithNothingToCheck(t *testing.T) {
	out, ok := runCheck(t, t.TempDir())
	if !ok {
		t.Fatalf("an empty dist failed the check:\n%s", out)
	}
	if !strings.Contains(out, "nothing to verify") {
		t.Errorf("output = %q, want it to say why it checked nothing", out)
	}
}
