// Package scripts holds tests for the release shell scripts, so the checks that
// gate a release are themselves covered by `go test ./...`.
package scripts

import (
	"bytes"
	"os"
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
	b, err := exec.Command("sh", "./check-winget-manifests.sh", dir).CombinedOutput()
	return string(b), err == nil
}

// TestWingetCheckAcceptsKomacManifests uses komac's real output for the
// RigSmith.ChangeRig 1.5.1 submission, captured byte for byte — root-level
// NestedInstallerType, one NestedInstallerFiles block, and CRLF line endings,
// which is what winget-pkgs stores.
//
// The CRLF matters: every `$`-anchored pattern in the check fails against a line
// ending in \r, which briefly made this check call komac's correct manifests
// non-portable. Both of this check's live failures have been an assumption about
// bytes it had never actually looked at, hence fixtures from both producers.
func TestWingetCheckAcceptsKomacManifests(t *testing.T) {
	// Guard the guard: git normalised this fixture to LF once already, which
	// would leave the assertion below passing against a file with no CRLF in it.
	// .gitattributes marks it -text; this fails loudly if that ever stops working.
	raw, err := os.ReadFile("testdata/winget-komac/RigSmith.ChangeRig.installer.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatal("fixture has lost its CRLF — it no longer tests what it exists to test (see .gitattributes)")
	}

	out, ok := runCheck(t, "testdata/winget-komac")
	if !ok {
		t.Fatalf("komac manifest rejected:\n%s", out)
	}
	if !strings.Contains(out, "portable (root-level)") {
		t.Errorf("output = %q, want the root-level shape recognised", out)
	}
	if !strings.Contains(out, "Commands present") {
		t.Errorf("output = %q, want Commands confirmed", out)
	}
}

// TestWingetCheckAcceptsGoReleaserManifests keeps the other producer covered:
// per-installer keys, LF endings. GoReleaser no longer submits, but the shape is
// still valid and switching back is one config line per package.
func TestWingetCheckAcceptsGoReleaserManifests(t *testing.T) {
	out, ok := runCheck(t, "testdata/winget-good")
	if !ok {
		t.Fatalf("GoReleaser-shaped manifest rejected:\n%s", out)
	}
	if !strings.Contains(out, "portable (per-installer)") {
		t.Errorf("output = %q, want the per-installer shape recognised", out)
	}
}

// TestWingetCheckCatchesANonPortableInstaller is the 23-day bug: komac reads
// clauderig.exe as an installer and writes `exe`, so winget unpacks the zip and
// puts nothing on PATH. winget-submit.sh corrects it before submitting; this
// pins that the check would catch it if the correction ever stopped working.
func TestWingetCheckCatchesANonPortableInstaller(t *testing.T) {
	out, ok := runCheck(t, "testdata/winget-bad")
	if ok {
		t.Fatalf("a non-portable installer passed:\n%s", out)
	}
	if !strings.Contains(out, "non-portable installer type") {
		t.Errorf("output = %q, want it to name the wrong type", out)
	}
}

// TestWingetCheckRequiresCommands pins the metadata regression that put
// Manifest-Metadata-Consistency on all five 1.5.1 submissions: `Commands` is
// what `winget search` and `winget install --command` read, and a manifest
// missing it installs fine while being harder to find.
func TestWingetCheckRequiresCommands(t *testing.T) {
	out, ok := runCheck(t, "testdata/winget-no-commands")
	if ok {
		t.Fatalf("a manifest with no Commands passed:\n%s", out)
	}
	if !strings.Contains(out, "no Commands") {
		t.Errorf("output = %q, want it to name the missing property", out)
	}
}

// TestWingetCheckIsQuietWithNothingToCheck: most runs generate no manifests at
// all, and that must not fail a build.
func TestWingetCheckIsQuietWithNothingToCheck(t *testing.T) {
	out, ok := runCheck(t, t.TempDir())
	if !ok {
		t.Fatalf("an empty directory failed the check:\n%s", out)
	}
	if !strings.Contains(out, "nothing to verify") {
		t.Errorf("output = %q, want it to say why it checked nothing", out)
	}
}
