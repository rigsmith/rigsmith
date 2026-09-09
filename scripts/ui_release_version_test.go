package scripts

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// uiVersion is what ui/go.mod declares, read the way the release does.
func uiVersion(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	out, err := exec.Command("sh", "./ui-version.sh", "../ui/go.mod").Output()
	if err != nil {
		t.Fatalf("ui-version.sh: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func releaseVersion(t *testing.T, ref string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	args := []string{"./ui-release-version.sh"}
	if ref != "" {
		args = append(args, ref)
	}
	out, err := exec.Command("sh", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// The tag names the version, and the module has to agree — that agreement is
// the only thing standing between a release and a window that reports one
// number under a tag promising another.
func TestUIReleaseVersionAcceptsTheModulesOwnTag(t *testing.T) {
	want := uiVersion(t)
	got, err := releaseVersion(t, "ui/v"+want)
	if err != nil {
		t.Fatalf("refused the module's own version: %v", err)
	}
	if got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
}

func TestUIReleaseVersionRefusesADisagreeingTag(t *testing.T) {
	if _, err := releaseVersion(t, "ui/v99.99.99"); err == nil {
		t.Error("a tag that disagrees with ui/go.mod was accepted — the release would ship two numbers")
	}
}

// The CLIs' tag fires a different workflow. Answering it with a version would
// mean this one could be run against a release that is not the window's.
func TestUIReleaseVersionRefusesACLITag(t *testing.T) {
	if _, err := releaseVersion(t, "v1.9.0"); err == nil {
		t.Error("v1.9.0 was accepted as a UI release tag")
	}
}

// A dry run has no tag, and its artifacts must never be mistaken for a
// release build.
func TestUIReleaseVersionMarksADryRun(t *testing.T) {
	got, err := releaseVersion(t, "")
	if err != nil {
		t.Fatalf("dry run refused: %v", err)
	}
	if want := uiVersion(t) + "-dryrun"; got != want {
		t.Errorf("dry run version = %q, want %q", got, want)
	}
}
