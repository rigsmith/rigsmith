// Port of the .NET rig's ConventionTests (the cases that live in the cli
// package): the defaultProject setter, rebuild scoping + dry-run, and
// runsettings auto-discovery. The ConfigWriter cases live in
// internal/config/writer_test.go and the slnx-before-sln solution-candidate
// case in internal/detect/solution_test.go, next to the code they exercise.
package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// writeTreeFile writes root/rel (slash-separated), creating parent dirs.
func writeTreeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// makeDir creates root/parts… and returns its path.
func makeDir(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const conventionExeCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup>
</Project>`

// ---- defaultProject setter (DefaultVerb) ----

func TestDefaultSetter_PersistsAMatchedRunnableProject(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", conventionExeCsproj)
	writeTreeFile(t, root, "App.slnx", `<Solution><Project Path="App/App.csproj" /></Solution>`)

	path, err := setDefaultProject(root, config.Config{}, "App")
	if err != nil {
		t.Fatalf("setDefaultProject: %v", err)
	}
	if want := filepath.Join(root, config.FileName); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProject != "App" {
		t.Fatalf("defaultProject = %q, want App", cfg.DefaultProject)
	}
}

func TestDefaultSetter_RejectsAnUnknownProject(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", conventionExeCsproj)
	writeTreeFile(t, root, "App.slnx", `<Solution><Project Path="App/App.csproj" /></Solution>`)

	if _, err := setDefaultProject(root, config.Config{}, "Nope"); err == nil {
		t.Fatal("want an error for an unknown project")
	}
	if _, err := os.Stat(filepath.Join(root, config.FileName)); !os.IsNotExist(err) {
		t.Fatalf(".rig.json exists (err=%v), want nothing written", err)
	}
}

// ---- rebuild scoped to discovered projects (RebuildVerb) ----

func TestRebuildTargetsOnlyDiscoveredProjectBinObjNotVendoredTrees(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", "<Project/>")
	makeDir(t, root, "App", "bin")
	makeDir(t, root, "App", "obj")
	makeDir(t, root, "vendor", "bin") // not a discovered project → must be left alone
	makeDir(t, root, "vendor", "obj")

	full := filepath.Join(root, "App", "App.csproj")
	projects := []detect.ProjectInfo{{Name: "App", RelPath: full, FullPath: full, OutputType: "Exe", Tfm: "net8.0"}}

	targets := rebuildTargetDirs(root, projects, nil)

	hasSuffix := func(suffix string) bool {
		for _, d := range targets {
			if strings.HasSuffix(d, suffix) {
				return true
			}
		}
		return false
	}
	if !hasSuffix(filepath.Join("App", "bin")) || !hasSuffix(filepath.Join("App", "obj")) {
		t.Fatalf("targets %v missing App/bin or App/obj", targets)
	}
	for _, d := range targets {
		if strings.Contains(d, "vendor") {
			t.Fatalf("targets %v include a vendored tree", targets)
		}
	}
}

func TestRebuildDryRunDeletesNothing(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", "<Project/>")
	writeTreeFile(t, root, "App.slnx", `<Solution><Project Path="App/App.csproj" /></Solution>`)
	bin := makeDir(t, root, "App", "bin")

	projects := detect.DiscoverDotNet(root, "", nil)
	removed := rebuildRemoveBinObj(io.Discard, root, projects, nil, true)

	if removed != 0 {
		t.Fatalf("removed = %d, want 0 on a dry run", removed)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("dry run must not delete anything: %v", err)
	}
}

// ---- runsettings auto-discovery (CoverageVerb.FindRunsettings) ----

func TestFindRunsettings_SingleNextToTestProjectIsUsed(t *testing.T) {
	root := t.TempDir()
	testDir := makeDir(t, root, "tests", "App.Tests")
	rs := writeTreeFile(t, root, "tests/App.Tests/CodeCoverage.runsettings", "<RunSettings/>")

	if got := findRunsettings(testDir, root); got != rs {
		t.Fatalf("findRunsettings = %q, want %q", got, rs)
	}
}

func TestFindRunsettings_AmbiguousReturnsNone(t *testing.T) {
	root := t.TempDir()
	dir := makeDir(t, root, "tests")
	writeTreeFile(t, root, "tests/a.runsettings", "<RunSettings/>")
	writeTreeFile(t, root, "tests/b.runsettings", "<RunSettings/>")

	if got := findRunsettings(dir, root); got != "" {
		t.Fatalf("findRunsettings = %q, want \"\" for an ambiguous dir", got)
	}
}

func TestFindRunsettings_NoneReturnsNone(t *testing.T) {
	root := t.TempDir()
	if got := findRunsettings(root, root); got != "" {
		t.Fatalf("findRunsettings = %q, want \"\"", got)
	}
}
