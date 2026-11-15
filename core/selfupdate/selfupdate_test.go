// Tests for `self-update`: the pure parts (version discovery, the installer
// argv, the reports) and the whole flow through Run with a fake Env. No network
// is touched and nothing is installed.
package selfupdate

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
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
// output, absent paths not installed.
func fakeBin(dir string, versions map[string]string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		if filepath.Dir(path) != dir {
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

func TestInstallerArgs_WholeFamilyPinnedToTheTagIntoTheBinDir(t *testing.T) {
	argv := InstallerArgs("rigsmith/rigsmith", "v1.28.0", "/home/you/.local/bin")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("argv = %v, want sh -c <script>", argv)
	}
	want := "curl -fsSL https://raw.githubusercontent.com/rigsmith/rigsmith/main/scripts/install.sh" +
		" | RIGSMITH_VERSION='v1.28.0' RIGSMITH_REPO='rigsmith/rigsmith'" +
		" RIGSMITH_BIN_DIR='/home/you/.local/bin' RIGSMITH_INSTALL='/home/you/.local' sh -s all"
	if argv[2] != want {
		t.Fatalf("script =\n %s\nwant\n %s", argv[2], want)
	}
}

func TestInstallerArgs_QuotesTheDirAndSkipsThePrefixOffABin(t *testing.T) {
	script := InstallerArgs("acme/fork", "v2.0.0", "/opt/you's tools")[2]
	if !strings.Contains(script, `RIGSMITH_BIN_DIR='/opt/you'\''s tools'`) {
		t.Fatalf("bin dir not shell-quoted: %s", script)
	}
	if strings.Contains(script, "RIGSMITH_INSTALL=") {
		t.Fatalf("a dir not named bin has no prefix to give an older installer: %s", script)
	}
	if !strings.Contains(script, "raw.githubusercontent.com/acme/fork/") || !strings.Contains(script, "RIGSMITH_REPO='acme/fork'") {
		t.Fatalf("fork repo not carried through: %s", script)
	}
}

func TestHomebrewManaged(t *testing.T) {
	for path, want := range map[string]bool{
		"/opt/homebrew/Caskroom/rigsmith/1.18.0/rig": true,
		"/usr/local/Cellar/rigsmith/1.18.0/bin/rig":  true,
		"/home/you/.local/bin/rig":                   false,
		"":                                           false,
	} {
		if got := HomebrewManaged(path); got != want {
			t.Errorf("HomebrewManaged(%q) = %v, want %v", path, got, want)
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

// testEnv is shiprig run from /home/you/.local/bin with the rest of the family
// behind it, and an installer that moves everything to the tag.
func testEnv(installed map[string]string, tag string) (*Env, *[][]string) {
	dir := "/home/you/.local/bin"
	var calls [][]string
	e := &Env{
		Tool: "shiprig", Version: "1.23.1", GOOS: "darwin", Repo: "rigsmith/rigsmith",
		Exe: dir + "/shiprig", Resolved: dir + "/shiprig",
		FetchLatestTag: func() (string, error) { return tag, nil },
		ProbeVersion:   fakeBin(dir, installed),
	}
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
	if want := InstallerArgs("rigsmith/rigsmith", "v1.28.0", "/home/you/.local/bin"); !reflect.DeepEqual((*calls)[0], want) {
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
		"no releases":       {func(e *Env) { e.FetchLatestTag = func() (string, error) { return "", nil } }, "no published releases"},
		"windows":           {func(e *Env) { e.GOOS = "windows" }, "can't replace a running shiprig.exe"},
		"homebrew":          {func(e *Env) { e.Resolved = "/opt/homebrew/Caskroom/rigsmith/1.18.0/shiprig" }, "brew upgrade --cask rigsmith"},
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
