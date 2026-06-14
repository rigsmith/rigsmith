package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

func TestParsePids(t *testing.T) {
	const self = 999
	tests := []struct {
		name  string
		input string
		want  []int
	}{
		{"empty", "", nil},
		{"single", "1234", []int{1234}},
		{"newline separated", "1234\n5678\n", []int{1234, 5678}},
		{"space separated", "12 34 56", []int{12, 34, 56}},
		{"dedup and sort", "30\n10\n20\n10\n", []int{10, 20, 30}},
		{"drops self", "100\n999\n200", []int{100, 200}},
		{"drops non-positive and junk", "0\n-3\nfoo\n42", []int{42}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePids(tt.input, self); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parsePids(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseNetstatPids(t *testing.T) {
	const self = 999
	// Real-ish `netstat -ano -p tcp` rows: Proto, Local, Foreign, State, PID.
	output := "" +
		"  Proto  Local Address          Foreign Address        State           PID\n" +
		"  TCP    0.0.0.0:3000           0.0.0.0:0              LISTENING       4321\n" +
		"  TCP    127.0.0.1:3000         0.0.0.0:0              LISTENING       4321\n" +
		"  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       5555\n" +
		"  TCP    127.0.0.1:3000         127.0.0.1:51000        ESTABLISHED     6000\n" +
		"  TCP    0.0.0.0:9999           0.0.0.0:0              LISTENING       999\n"

	if got := parseNetstatPids(output, 3000, self); !reflect.DeepEqual(got, []int{4321}) {
		t.Fatalf("port 3000 = %v, want [4321] (dedup, only LISTENING on :3000)", got)
	}
	if got := parseNetstatPids(output, 8080, self); !reflect.DeepEqual(got, []int{5555}) {
		t.Fatalf("port 8080 = %v, want [5555]", got)
	}
	// Port 9999 is owned by self, so it must be dropped.
	if got := parseNetstatPids(output, 9999, self); got != nil {
		t.Fatalf("port 9999 = %v, want nil (self filtered)", got)
	}
	// A port nobody listens on.
	if got := parseNetstatPids(output, 1234, self); got != nil {
		t.Fatalf("port 1234 = %v, want nil", got)
	}
}

// ---- Windows CIM command-line matching (ported from the .NET rig's
// VerbLogicTests: Kill_parses_tab_delimited_pid_and_command_line /
// Kill_matches_driver_and_apphost_but_not_self_or_unrelated). ----

func TestParseProcessList_TabDelimitedPidAndCommandLine(t *testing.T) {
	// PID<tab>CommandLine, CRLF endings, a command-line-less system process,
	// and a blank line — exactly the shape the CIM query emits.
	output := "1001\tC:\\dotnet.exe run --project C:\\src\\App\\App.csproj\r\n" +
		"1002\tC:\\src\\App\\bin\\Debug\\net8.0\\App.exe\r\n" +
		"4\t\r\n" +
		"\r\n"
	procs := parseProcessList(output)

	if len(procs) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(procs), procs)
	}
	if procs[0] != (processEntry{1001, `C:\dotnet.exe run --project C:\src\App\App.csproj`}) {
		t.Fatalf("procs[0] = %+v", procs[0])
	}
	if procs[2] != (processEntry{4, ""}) { // system process, empty command line
		t.Fatalf("procs[2] = %+v", procs[2])
	}
}

func TestParseProcessList_SkipsUnparseableLines(t *testing.T) {
	output := "garbage\nWARNING: something\t/usr/bin/x\n  42 \t/bin/app\n"
	procs := parseProcessList(output)
	if len(procs) != 1 || procs[0] != (processEntry{42, "/bin/app"}) {
		t.Fatalf("procs = %+v, want only (42, /bin/app)", procs)
	}
}

func TestMatchProcesses_DriverAndApphostButNotSelfOrUnrelated(t *testing.T) {
	procs := []processEntry{
		{1001, `C:\dotnet.exe run --project C:\src\App\App.csproj`}, // the run/watch driver
		{1002, `C:\src\App\bin\Debug\net8.0\App.exe`},               // the apphost
		{1003, `C:\dotnet.exe run --project C:\src\Other\Other.csproj`},
		{4, ""},               // system process
		{777, "rig kill App"}, // ourselves
	}

	matched := matchProcesses(procs, "App", 777)
	var pids []int
	for _, p := range matched {
		pids = append(pids, p.Pid)
	}
	if !reflect.DeepEqual(pids, []int{1001, 1002}) { // driver + apphost; not Other, system, or self
		t.Fatalf("matched PIDs = %v, want [1001 1002]", pids)
	}
}

func TestMatchProcesses_IsCaseInsensitive(t *testing.T) {
	procs := []processEntry{{10, `C:\src\APP\app.exe`}}
	if got := matchProcesses(procs, "app", 1); len(got) != 1 {
		t.Fatalf("case-insensitive match failed: %v", got)
	}
	if got := matchProcesses(procs, "missing", 1); len(got) != 0 {
		t.Fatalf("unexpected match: %v", got)
	}
}

func TestEncodePowerShell_IsBase64OverUTF16LE(t *testing.T) {
	// "ab" → UTF-16LE 61 00 62 00 → base64 "YQBiAA==".
	if got := encodePowerShell("ab"); got != "YQBiAA==" {
		t.Fatalf("encodePowerShell(ab) = %q, want YQBiAA==", got)
	}
}

func TestMatchProjectNames(t *testing.T) {
	names := []string{"Buoy.Web.Api", "Buoy.Web.Worker", "Buoy.Core"}
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"exact wins over substring", "Buoy.Core", []string{"Buoy.Core"}},
		{"exact case-insensitive", "buoy.core", []string{"Buoy.Core"}},
		{"substring fallback", "Web", []string{"Buoy.Web.Api", "Buoy.Web.Worker"}},
		{"no match", "nope", nil},
		{"empty query", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchProjectNames(names, tt.query); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("matchProjectNames(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestDirBase(t *testing.T) {
	tests := map[string]string{
		"/Users/john/Git/net-changesets":  "net-changesets",
		"/Users/john/Git/net-changesets/": "net-changesets",
		"net-changesets":                  "net-changesets",
		`C:\Users\john\repo`:              "repo",
		"/":                               "",
	}
	for in, want := range tests {
		if got := dirBase(in); got != want {
			t.Fatalf("dirBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinInts(t *testing.T) {
	if got := joinInts([]int{3000, 8080}, ", "); got != "3000, 8080" {
		t.Fatalf("joinInts = %q, want %q", got, "3000, 8080")
	}
	if got := joinInts(nil, ", "); got != "" {
		t.Fatalf("joinInts(nil) = %q, want empty", got)
	}
}

func TestResolveKillPatterns_ConfigMatchWins(t *testing.T) {
	// With no name arg and an explicit kill.match, the config wins outright and
	// no discovery happens. Use a temp dir with no manifests as root.
	cfg := config.Config{Kill: config.Kill{Match: []string{"dotnet watch", "vite"}}}
	got := resolveKillPatterns(cfg, t.TempDir(), "")
	if !reflect.DeepEqual(got, []string{"dotnet watch", "vite"}) {
		t.Fatalf("resolveKillPatterns = %v, want config match", got)
	}
}

func TestResolveKillPatterns_ConfigMatchWinsEvenOverAnArg(t *testing.T) {
	// Config kill.match still wins over a name arg (it may pin non-project
	// processes), matching the .NET rig.
	cfg := config.Config{Kill: config.Kill{Match: []string{"Pinned"}}, DefaultProject: "Other"}
	got := resolveKillPatterns(cfg, t.TempDir(), "MicaSpike")
	if !reflect.DeepEqual(got, []string{"Pinned"}) {
		t.Fatalf("resolveKillPatterns = %v, want [Pinned]", got)
	}
}

func TestResolveKillPatterns_UnmatchedArgIsHonoredAsARawPattern(t *testing.T) {
	// A name that matches nothing is honored as-is, so you can still
	// `rig kill SomeExternalProc`.
	got := resolveKillPatterns(config.Config{}, t.TempDir(), "ghost")
	if !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Fatalf("resolveKillPatterns = %v, want [ghost]", got)
	}
}

func TestDotnetKillPatterns_BareKillSweepsEveryRunnableProject(t *testing.T) {
	// The "stop everything I started" sweep — all runnables, libs/tests excluded.
	projects := []detect.ProjectInfo{
		{Name: "App", OutputType: "Exe"},
		{Name: "MicaSpike", OutputType: "WinExe"},
		{Name: "Core"}, // library
		{Name: "App.Tests", OutputType: "Exe", IsTest: true}, // test host
	}
	got := dotnetKillPatterns(projects, "")
	if !reflect.DeepEqual(got, []string{"App", "MicaSpike"}) {
		t.Fatalf("sweep = %v, want [App MicaSpike]", got)
	}
	// Only libraries → nothing to sweep (never fall back to something broader).
	if got := dotnetKillPatterns([]detect.ProjectInfo{{Name: "OnlyLib"}}, ""); got != nil {
		t.Fatalf("lib-only sweep = %v, want nil", got)
	}
}

func TestDotnetKillPatterns_WithArgTargetsTheNamedProject(t *testing.T) {
	projects := []detect.ProjectInfo{
		{Name: "Acme.App", OutputType: "Exe"},
		{Name: "Acme.MicaSpike", OutputType: "Exe"},
		{Name: "Core"},
	}
	// short-name exact wins, then substring.
	if got := dotnetKillPatterns(projects, "MicaSpike"); !reflect.DeepEqual(got, []string{"Acme.MicaSpike"}) {
		t.Fatalf("exact = %v, want [Acme.MicaSpike]", got)
	}
	if got := dotnetKillPatterns(projects, "Acme"); !reflect.DeepEqual(got, []string{"Acme.App", "Acme.MicaSpike"}) {
		t.Fatalf("substring = %v, want both Acme projects", got)
	}
	// No match → nil, so the caller can honor the raw string.
	if got := dotnetKillPatterns(projects, "ghost"); got != nil {
		t.Fatalf("no match = %v, want nil", got)
	}
}

func TestDotnetKillPatterns_PatternIsTheProjectNameNotTheAssemblyName(t *testing.T) {
	// Both platforms match the full command line, so the (narrower) project name
	// is the target — present in the `dotnet run --project` cmdline and the
	// apphost path — never the AssemblyName.
	app := detect.ProjectInfo{Name: "App", OutputType: "Exe", AssemblyName: "AcmeApp"}
	if got := dotnetKillPatterns([]detect.ProjectInfo{app}, ""); !reflect.DeepEqual(got, []string{"App"}) {
		t.Fatalf("sweep = %v, want [App] (the project name, not AcmeApp)", got)
	}
}

func TestResolveKillPatterns_DotnetRepoSweepsRunnableProjectNames(t *testing.T) {
	// End-to-end through discovery: a .NET repo's bare sweep targets the
	// runnable projects' names only.
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Repo.slnx", `<Solution><Project Path="App/App.csproj" /><Project Path="Core/Core.csproj" /></Solution>`)
	write("App/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	write("Core/Core.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)

	if got := resolveKillPatterns(config.Config{}, root, ""); !reflect.DeepEqual(got, []string{"App"}) {
		t.Fatalf("sweep = %v, want [App]", got)
	}
}

func TestTruncateLabel(t *testing.T) {
	if got := truncateLabel("short", 90); got != "short" {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	long := strings.Repeat("a", 100)
	got := truncateLabel(long, 10)
	if len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate(len100, 10) = %q (len %d)", got, len([]rune(got)))
	}
}
