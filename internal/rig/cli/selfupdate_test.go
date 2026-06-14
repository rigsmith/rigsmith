// Tests for the pure parts of `rig self-update` (the .NET rig's UpdateVerb,
// re-targeted from NuGet to GitHub Releases + scripts/install.sh). Version
// comparison (latestStable/isNewer) is covered with the other UpdateVerb ports
// in dotnetverbs_test.go. No network is touched here.
package cli

import (
	"strings"
	"testing"
)

func TestResolveUpdateRepo_EnvOverridesTheBuildDefault(t *testing.T) {
	if got := resolveUpdateRepo(""); got != "rigsmith/rigsmith" {
		t.Fatalf("default repo = %q, want rigsmith/rigsmith (matching scripts/install.sh)", got)
	}
	if got := resolveUpdateRepo("  "); got != "rigsmith/rigsmith" {
		t.Fatalf("blank env = %q, want the default", got)
	}
	if got := resolveUpdateRepo("acme/fork"); got != "acme/fork" {
		t.Fatalf("env override = %q, want acme/fork", got)
	}
}

func TestLatestReleaseURL(t *testing.T) {
	want := "https://api.github.com/repos/rigsmith/rigsmith/releases/latest"
	if got := latestReleaseURL("rigsmith/rigsmith"); got != want {
		t.Fatalf("latestReleaseURL = %q, want %q", got, want)
	}
}

func TestParseReleaseTag(t *testing.T) {
	if got := parseReleaseTag([]byte(`{"tag_name":"v1.4.0","name":"v1.4.0"}`)); got != "v1.4.0" {
		t.Fatalf("tag = %q, want v1.4.0", got)
	}
	if got := parseReleaseTag([]byte(`{"message":"Not Found"}`)); got != "" {
		t.Fatalf("missing tag = %q, want empty", got)
	}
	if got := parseReleaseTag([]byte("not json")); got != "" {
		t.Fatalf("malformed = %q, want empty", got)
	}
}

func TestReleaseAssetName_MatchesGoreleaserAndInstallSh(t *testing.T) {
	// <bin>_<version-without-v>_<os>_<arch>.tar.gz — the install.sh shape.
	if got := releaseAssetName("rig", "v1.2.3", "darwin", "arm64"); got != "rig_1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("asset = %q", got)
	}
	if got := releaseAssetName("rig", "1.2.3", "linux", "amd64"); got != "rig_1.2.3_linux_amd64.tar.gz" {
		t.Fatalf("asset (no v) = %q", got)
	}
	// Windows archives are zips (goreleaser format_overrides).
	if got := releaseAssetName("rig", "v1.2.3", "windows", "amd64"); got != "rig_1.2.3_windows_amd64.zip" {
		t.Fatalf("windows asset = %q", got)
	}
}

func TestSelfUpdateInstallerArgs_RerunsTheInstallerPinnedToTheTag(t *testing.T) {
	argv := selfUpdateInstallerArgs("rigsmith/rigsmith", "v1.4.0")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("argv = %v, want sh -c <script>", argv)
	}
	script := argv[2]
	for _, want := range []string{
		"https://raw.githubusercontent.com/rigsmith/rigsmith/main/scripts/install.sh",
		"RIGSMITH_VERSION=v1.4.0",
		"sh -s rig", // only rig, never the sibling binaries
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script %q missing %q", script, want)
		}
	}
}
