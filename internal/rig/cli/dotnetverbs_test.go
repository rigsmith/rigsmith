// Port of the .NET rig's VerbLogicTests: project resolution and argv building
// for run/test/rebuild/publish/add/remove/global/dlx/self-update/outdated and
// the Windows cmd.exe escaping, against the pure functions in dotnetverbs.go.
package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

func dnExe(name string) detect.ProjectInfo {
	return detect.ProjectInfo{
		Name: name, RelPath: name + "/" + name + ".csproj",
		FullPath: "/r/" + name + "/" + name + ".csproj", OutputType: "Exe", Tfm: "net8.0",
	}
}

func dnLib(name string) detect.ProjectInfo {
	return detect.ProjectInfo{
		Name: name, RelPath: name + "/" + name + ".csproj",
		FullPath: "/r/" + name + "/" + name + ".csproj", Tfm: "net8.0",
	}
}

// indexOfArg returns the index of s in xs, -1 when absent.
func indexOfArg(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// wantConsecutive fails unless seq appears consecutively in xs.
func wantConsecutive(t *testing.T, xs []string, seq ...string) {
	t.Helper()
outer:
	for i := 0; i+len(seq) <= len(xs); i++ {
		for j, s := range seq {
			if xs[i+j] != s {
				continue outer
			}
		}
		return
	}
	t.Fatalf("args %v do not contain %v consecutively", xs, seq)
}

// ---- resolveRunProject (RunVerb.Resolve) ----

func TestRunResolve_NoQuerySingleRunnableIsSelected(t *testing.T) {
	res := resolveRunProject([]detect.ProjectInfo{dnExe("App"), dnLib("Core")}, "", "")
	if res.Selected == nil || res.Selected.Name != "App" {
		t.Fatalf("Selected = %+v, want App", res.Selected)
	}
}

func TestRunResolve_NoQueryPrefersDefaultProject(t *testing.T) {
	res := resolveRunProject([]detect.ProjectInfo{dnExe("App"), dnExe("Tool")}, "", "Tool")
	if res.Selected == nil || res.Selected.Name != "Tool" {
		t.Fatalf("Selected = %+v, want Tool", res.Selected)
	}
}

func TestRunResolve_NoQueryMultipleRunnablesIsAmbiguous(t *testing.T) {
	res := resolveRunProject([]detect.ProjectInfo{dnExe("App"), dnExe("Tool")}, "", "")
	if res.Selected != nil {
		t.Fatalf("Selected = %+v, want nil", res.Selected)
	}
	if len(res.Ambiguous) != 2 {
		t.Fatalf("Ambiguous = %d, want 2", len(res.Ambiguous))
	}
}

func TestRunResolve_QueryMatchesShortNameThenSubstring(t *testing.T) {
	projects := []detect.ProjectInfo{dnExe("Acme.App"), dnExe("Acme.Tool")}

	if res := resolveRunProject(projects, "App", ""); res.Selected == nil || res.Selected.Name != "Acme.App" {
		t.Fatalf("short-name exact: Selected = %+v, want Acme.App", res.Selected)
	}
	if res := resolveRunProject(projects, "Acme", ""); len(res.Ambiguous) != 2 {
		t.Fatalf("substring: Ambiguous = %d, want 2", len(res.Ambiguous))
	}
	if res := resolveRunProject(projects, "nope", ""); res.Err == "" {
		t.Fatal("no match: want an error")
	}
}

func TestRunResolve_NoRunnableProjectsIsAnError(t *testing.T) {
	if res := resolveRunProject([]detect.ProjectInfo{dnLib("Core")}, "", ""); res.Err == "" {
		t.Fatal("want an error when nothing is runnable")
	}
}

// ---- buildDotnetRunArgs / buildDotnetTestArgs ----

func TestRunArgs_FrameworkAndLaunchProfileBeforeTheForwardingBoundary(t *testing.T) {
	args := buildDotnetRunArgs("/r/App/App.csproj", "Release", "net10.0", "https",
		[]string{"--urls", "http://*:0"}, false)

	wantConsecutive(t, args, "run", "--project", "/r/App/App.csproj")
	wantConsecutive(t, args, "--framework", "net10.0")
	wantConsecutive(t, args, "--launch-profile", "https")
	// forwarded args live after `--`, and the framework flags come before it
	if indexOfArg(args, "--framework") >= indexOfArg(args, "--") {
		t.Fatalf("--framework must come before --: %v", args)
	}
	after := args[indexOfArg(args, "--")+1:]
	wantConsecutive(t, after, "--urls", "http://*:0")
}

func TestRunArgs_OmitUnsetOptionsAndPrependWatch(t *testing.T) {
	args := buildDotnetRunArgs("/r/App/App.csproj", "", "", "", nil, true)

	want := []string{"watch", "run", "--project", "/r/App/App.csproj"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestTestArgs_IncludeFrameworkAndFilter(t *testing.T) {
	args := buildDotnetTestArgs(vsTestRunner, "/r/T/T.csproj", "FullyQualifiedName~Foo",
		"net10.0", []string{"--blame"}, false, "")

	wantConsecutive(t, args, "test", "/r/T/T.csproj")
	wantConsecutive(t, args, "--framework", "net10.0")
	wantConsecutive(t, args, "--filter", "FullyQualifiedName~Foo")
	if indexOfArg(args, "--blame") < 0 {
		t.Fatalf("args = %v, want --blame forwarded", args)
	}
}

func TestTestArgs_PassTheProjectPositionallyForVsTest(t *testing.T) {
	args := buildDotnetTestArgs(vsTestRunner, "/r/T/T.csproj", "", "", nil, false, "")
	// Classic VSTest `dotnet test` has no `--project` switch — positional only.
	want := []string{"test", "/r/T/T.csproj"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestTestArgs_PassTheProjectViaFlagForMtp(t *testing.T) {
	args := buildDotnetTestArgs(mtpRunner, "/r/T/T.csproj", "FullyQualifiedName~Foo", "", nil, false, "")
	// The MTP `dotnet test` parser names the project with `--project`; the same
	// `--filter` expression rides along unchanged.
	wantConsecutive(t, args, "test", "--project", "/r/T/T.csproj")
	wantConsecutive(t, args, "--filter", "FullyQualifiedName~Foo")
}

func TestTestAndBuildArgs_CarryConfiguration(t *testing.T) {
	args := buildDotnetTestArgs(vsTestRunner, "/r/T/T.csproj", "", "", nil, false, "Release")
	wantConsecutive(t, args, "-c", "Release")
}

func TestTestShorthandFilter_MapsLeadingOperators(t *testing.T) {
	tests := map[string]string{
		"~Foo":   "FullyQualifiedName~Foo",
		"=Foo":   "FullyQualifiedName=Foo",
		"!~Foo":  "FullyQualifiedName!~Foo",
		"!=Foo":  "FullyQualifiedName!=Foo",
		"Foo":    "",
		"!":      "",
		"":       "",
		"--blah": "",
	}
	for in, want := range tests {
		if got := testShorthandFilter(in); got != want {
			t.Fatalf("testShorthandFilter(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- rebuild guards (RebuildVerb.IsSkipped / IsWithinRoot) ----

func TestRebuildSkip_MatchesExactAndPrefixSegments(t *testing.T) {
	skip := []string{"vendor", "node_modules"}
	tests := []struct {
		dir  string
		want bool
	}{
		{"vendor/bin", true},
		{"vendor", true},
		{"src/App/bin", false},
		{"vendored/bin", false}, // not a path segment
	}
	for _, tt := range tests {
		if got := rebuildIsSkipped(tt.dir, skip); got != tt.want {
			t.Fatalf("rebuildIsSkipped(%q) = %v, want %v", tt.dir, got, tt.want)
		}
	}
}

func TestRebuildWithinRoot_GuardsTheRecursiveDelete(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		path string
		want bool
	}{
		{root + "/src/App/bin", true},
		{root, false},              // never the root itself
		{root + "/../evil", false}, // `..` escape
		{t.TempDir(), false},       // a sibling tree
	}
	for _, tt := range tests {
		if got := rebuildIsWithinRoot(root, tt.path); got != tt.want {
			t.Fatalf("rebuildIsWithinRoot(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
		}
	}
}

// ---- winCmdArguments (Exec.WinCmdArguments) ----

func TestWinCmdArguments_CaretEscapeMetacharacters(t *testing.T) {
	line := winCmdArguments("rig-node.cmd", []string{"[suggest:5]", "a & echo pwned"})
	if !strings.HasPrefix(line, `/d /s /c "`) {
		t.Fatalf("line = %q, want /d /s /c \" prefix", line)
	}
	if !strings.Contains(line, "^^^&") { // the ampersand is caret-escaped…
		t.Fatalf("line = %q, want ^^^&", line)
	}
	if strings.Contains(line, " & echo") { // …so it can't run as its own command
		t.Fatalf("line = %q must not contain a bare ' & echo'", line)
	}

	// Exact escaping must match the Node tool's winCmdInvocation (whose output
	// is validated against real cmd.exe by a win32 integration test).
	want := `/d /s /c "x.cmd ^^^"a^^^&b^^^""`
	if got := winCmdArguments("x.cmd", []string{"a&b"}); got != want {
		t.Fatalf("winCmdArguments = %q, want %q", got, want)
	}
}

// ---- publish (PublishVerb) ----

func TestPublish_RidAndOutputDefaultsAndOverrides(t *testing.T) {
	if got := resolvePublishOutput(nil, "osx-arm64"); got != "dist/osx-arm64" {
		t.Fatalf("default output = %q, want dist/osx-arm64", got)
	}
	if got := resolvePublishOutput(&config.Publish{Output: "out/{rid}/app"}, "win-x64"); got != "out/win-x64/app" {
		t.Fatalf("templated output = %q, want out/win-x64/app", got)
	}
	if got := resolvePublishRid(&config.Publish{Rid: "linux-x64"}); got != "linux-x64" {
		t.Fatalf("configured rid = %q, want linux-x64", got)
	}
	if got := resolvePublishRid(nil); got == "" || !strings.Contains(got, "-") {
		t.Fatalf("host rid = %q, want a <os>-<arch> identifier", got)
	}
}

func TestPublishArgs_ReflectConfigurationSelfContainedAndSingleFile(t *testing.T) {
	args := buildPublishArgs("/r/App/App.csproj", "Debug", "win-x64", false, true, "/r/dist/win-x64")

	wantConsecutive(t, args, "publish", "/r/App/App.csproj", "-c", "Debug", "-r", "win-x64")
	wantConsecutive(t, args, "--self-contained", "false")
	if indexOfArg(args, "-p:PublishSingleFile=true") < 0 {
		t.Fatalf("args = %v, want -p:PublishSingleFile=true", args)
	}
	wantConsecutive(t, args, "-o", "/r/dist/win-x64")
}

// ---- resolveAddTarget (AddVerb.ResolveTarget) ----

func TestAddTargets_DefaultThenSoleThenAmbiguous(t *testing.T) {
	// default project wins (and add spans libs too, not just runnables)
	res := resolveAddTarget([]detect.ProjectInfo{dnExe("App"), dnLib("Core")}, "", "Core")
	if res.Selected == nil || res.Selected.Name != "Core" {
		t.Fatalf("default: Selected = %+v, want Core", res.Selected)
	}

	// single project → chosen with no prompt
	res = resolveAddTarget([]detect.ProjectInfo{dnLib("OnlyLib")}, "", "")
	if res.Selected == nil || res.Selected.Name != "OnlyLib" {
		t.Fatalf("sole: Selected = %+v, want OnlyLib", res.Selected)
	}

	// several, no default → ambiguous (caller prompts / errors)
	res = resolveAddTarget([]detect.ProjectInfo{dnExe("App"), dnLib("Core")}, "", "")
	if res.Selected != nil || len(res.Ambiguous) != 2 {
		t.Fatalf("ambiguous: got %+v", res)
	}

	// explicit query that doesn't match → error
	if res = resolveAddTarget([]detect.ProjectInfo{dnExe("App")}, "nope", ""); res.Err == "" {
		t.Fatal("non-matching query: want an error")
	}
}

// ---- ni-parity dependency verbs (RemoveVerb / GlobalVerb / DlxVerb) ----

func TestRemove_BuildsDotnetRemovePackageArgs(t *testing.T) {
	args := buildRemovePackageArgs("/repo/App/App.csproj", "Newtonsoft.Json", nil)
	wantConsecutive(t, args, "remove", "/repo/App/App.csproj", "package", "Newtonsoft.Json")

	// Project selection reuses resolveAddTarget (covered above); forwarded args trail.
	args = buildRemovePackageArgs("/p.csproj", "Pkg", []string{"--interactive"})
	if args[len(args)-1] != "--interactive" {
		t.Fatalf("args = %v, want --interactive last", args)
	}
}

func TestGlobal_BuildsDotnetToolInstallGlobalArgs(t *testing.T) {
	args := buildGlobalToolArgs("dotnet-ef", nil)
	wantConsecutive(t, args, "tool", "install", "--global", "dotnet-ef")

	args = buildGlobalToolArgs("dotnet-ef", []string{"--version", "9.0.0"})
	wantConsecutive(t, args, "--version", "9.0.0")
}

func TestDlx_BuildsDnxArgsToolFirst(t *testing.T) {
	if args := buildDlxArgs("dotnetsay", nil); !reflect.DeepEqual(args, []string{"dotnetsay"}) {
		t.Fatalf("args = %v, want [dotnetsay]", args)
	}
	wantConsecutive(t, buildDlxArgs("dotnetsay", []string{"hello", "world"}),
		"dotnetsay", "hello", "world")
}

func TestDlx_DnxAvailabilityCheckDoesNotPanic(t *testing.T) {
	// Pure PATH probe — the result depends on the machine, but it must never panic.
	_ = dnxAvailable()
}

// ---- self-update version logic (UpdateVerb) ----

func TestUpdate_LatestStableIgnoresPrereleases(t *testing.T) {
	if got := latestStable([]string{"0.1.0", "1.0.0", "1.1.0", "1.2.0-beta", "0.9.0"}); got != "1.1.0" {
		t.Fatalf("latestStable = %q, want 1.1.0", got)
	}
	if got := latestStable([]string{"2.0.0-rc1", "1.5.0"}); got != "1.5.0" {
		t.Fatalf("latestStable = %q, want 1.5.0", got)
	}
	if got := latestStable([]string{"1.0.0-alpha"}); got != "" { // only prereleases
		t.Fatalf("latestStable = %q, want empty", got)
	}
	if got := latestStable(nil); got != "" {
		t.Fatalf("latestStable(nil) = %q, want empty", got)
	}
}

func TestUpdate_IsNewerComparesAndTreatsUnknownCurrentAsOutdated(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.1.0", true},
		{"1.1.0", "1.1.0", false},
		{"1.2.0", "1.1.0", false},
		{"1.1.0+abc123", "1.2.0", true}, // build metadata stripped
		{"", "1.1.0", true},             // unknown current → offer update
		{"1.0.0", "garbage", false},     // unparseable latest → no
	}
	for _, tt := range tests {
		if got := isNewer(tt.current, tt.latest); got != tt.want {
			t.Fatalf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestUpdate_SiblingArgsAlwaysCarrySelfOnly(t *testing.T) {
	// The cross-update hands off with --self-only so the sibling never bounces
	// back and re-updates us (infinite mutual recursion).
	if got := siblingSelfUpdateArgs(false); !reflect.DeepEqual(got, []string{"self-update", "--self-only"}) {
		t.Fatalf("siblingSelfUpdateArgs(false) = %v", got)
	}
	if got := siblingSelfUpdateArgs(true); !reflect.DeepEqual(got, []string{"self-update", "--check", "--self-only"}) {
		t.Fatalf("siblingSelfUpdateArgs(true) = %v", got)
	}
}

// ---- buildOutdatedArgs (OutdatedVerb.BuildArgs) ----

func TestOutdatedArgs_DefaultToOutdatedLensWithSolution(t *testing.T) {
	args := buildOutdatedArgs("/r/App.slnx", false, false, false, false, nil)
	wantConsecutive(t, args, "list", "/r/App.slnx", "package", "--outdated")
}

func TestOutdatedLenses_AreMutuallyExclusiveAndPrereleaseIsOutdatedOnly(t *testing.T) {
	// vulnerable wins over the default outdated; prerelease is dropped (not valid there)
	vuln := buildOutdatedArgs("", true, false, true, true, nil)
	if indexOfArg(vuln, "--vulnerable") < 0 || indexOfArg(vuln, "--include-transitive") < 0 {
		t.Fatalf("args = %v, want --vulnerable and --include-transitive", vuln)
	}
	if indexOfArg(vuln, "--outdated") >= 0 || indexOfArg(vuln, "--include-prerelease") >= 0 {
		t.Fatalf("args = %v must not contain --outdated / --include-prerelease", vuln)
	}

	// prerelease applies on the default outdated lens
	if args := buildOutdatedArgs("", false, false, false, true, nil); indexOfArg(args, "--include-prerelease") < 0 {
		t.Fatalf("args = %v, want --include-prerelease", args)
	}
}
