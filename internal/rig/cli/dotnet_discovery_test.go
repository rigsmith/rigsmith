// The .NET dev discovery and run-target selection. The regression: the dev
// surfaces (run/info/kill/cd) discovered .NET projects through the ecosystem
// adapter's release-oriented Discover, which drops projects without a <Version>
// — so a version-less app was invisible. discoverWorkspace now sources .NET
// from the convention-first dev model instead.
package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// versionless project fixtures — none declare a <Version>, the regression case.
const (
	discoLibCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
</Project>`
	discoTestCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk" Version="18.0.0" /></ItemGroup>
</Project>`
)

func targetByName(t *testing.T, ts []target, name string) target {
	t.Helper()
	for _, x := range ts {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("no target named %q in %v", name, names(ts))
	return target{}
}

func TestDiscoverWorkspace_DotnetVersionlessProjectsDiscoveredAndClassified(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RIG_GLOBAL_CONFIG", filepath.Join(root, "global-rig.json"))
	writeTreeFile(t, root, "App/App.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup><ProjectReference Include="..\Lib\Lib.csproj" /></ItemGroup>
</Project>`)
	writeTreeFile(t, root, "Lib/Lib.csproj", discoLibCsproj)
	writeTreeFile(t, root, "App.Tests/App.Tests.csproj", discoTestCsproj)
	writeTreeFile(t, root, "App.slnx", `<Solution>
  <Project Path="App/App.csproj" />
  <Project Path="Lib/Lib.csproj" />
  <Project Path="App.Tests/App.Tests.csproj" />
</Solution>`)

	ts := discoverWorkspace(context.Background(), root, nil)
	if len(ts) != 3 { // pre-fix: 0 (none declare a version)
		t.Fatalf("got %d targets, want 3: %v", len(ts), names(ts))
	}

	app := targetByName(t, ts, "App")
	if app.Eco != detect.DotNet || !app.Runnable || app.IsTest {
		t.Errorf("App = %+v, want runnable .NET non-test", app)
	}
	if len(app.Deps) != 1 || app.Deps[0] != "Lib" {
		t.Errorf("App.Deps = %v, want [Lib] (intra-repo project reference)", app.Deps)
	}
	if isRunnable(targetByName(t, ts, "Lib")) {
		t.Error("Lib (library) should not be runnable")
	}
	tests := targetByName(t, ts, "App.Tests")
	if !tests.IsTest || isRunnable(tests) {
		t.Errorf("App.Tests = %+v, want a non-runnable test project", tests)
	}
}

func TestPreferredRunTask_MatchesDefaultByFullOrShortName(t *testing.T) {
	tasks := []allTask{
		{name: "Halyards.Desktop", argv: []string{"dotnet", "run"}},
		{name: "Halyards.JobTread", argv: []string{"dotnet", "run"}},
	}
	if got, ok := preferredRunTask(tasks, "Halyards.JobTread"); !ok || got.name != "Halyards.JobTread" {
		t.Errorf("full-name match: got %v ok=%v", got.name, ok)
	}
	if got, ok := preferredRunTask(tasks, "Desktop"); !ok || got.name != "Halyards.Desktop" {
		t.Errorf("short-name match: got %v ok=%v", got.name, ok)
	}
	if _, ok := preferredRunTask(tasks, ""); ok {
		t.Error("empty default should not match")
	}
	if _, ok := preferredRunTask(tasks, "Nope"); ok {
		t.Error("unknown default should not match")
	}
}

func TestOfferRunChoice_DefaultProjectShortCircuitsThePicker(t *testing.T) {
	// Two runnable tasks with distinguishable argv; non-interactive (no TTY).
	// Without a default this is ambiguous → error; the configured default must
	// run directly instead.
	tasks := []allTask{
		{name: "App", dir: t.TempDir(), argv: []string{"dotnet", "run", "--project", "App"}},
		{name: "Tool", dir: t.TempDir(), argv: []string{"dotnet", "run", "--project", "Tool"}},
	}

	defer func(p bool) { dryRun = p }(dryRun)
	dryRun = true

	// No default → ambiguous, returns guidance rather than running.
	cmd, buf := newRunHost()
	if _, err := offerRunChoice(cmd, tasks, nil, "", false); err == nil || !strings.Contains(err.Error(), "run target") {
		t.Fatalf("no default: err = %v, want the ambiguous-target guidance", err)
	}

	// Default names Tool → runs it directly, no picker.
	cmd, buf = newRunHost()
	if _, err := offerRunChoice(cmd, tasks, nil, "Tool", false); err != nil {
		t.Fatalf("with default: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "dotnet run --project Tool") {
		t.Fatalf("echo = %q, want the default (Tool) to run directly", got)
	}
}
