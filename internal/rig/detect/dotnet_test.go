package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file with content, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exeCsproj(tfm string) string {
	return `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>` + tfm + `</TargetFramework>
  </PropertyGroup>
</Project>`
}

const libCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
</Project>`

const testCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.NET.Test.Sdk" Version="18.0.0" />
  </ItemGroup>
</Project>`

func byName(t *testing.T, projects []ProjectInfo, name string) ProjectInfo {
	t.Helper()
	for _, p := range projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no project named %q in %v", name, projects)
	return ProjectInfo{}
}

// Ported from the .NET rig's ProjectDiscoveryTests.

func TestDiscoverDotNet_DiscoversAndClassifiesEveryProject(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net9.0"))
	writeFile(t, filepath.Join(tmp, "Lib", "Lib.csproj"), libCsproj)
	writeFile(t, filepath.Join(tmp, "App.Tests", "App.Tests.csproj"), testCsproj)
	writeFile(t, filepath.Join(tmp, "App.slnx"), `<Solution>
  <Project Path="App/App.csproj" />
  <Project Path="Lib/Lib.csproj" />
  <Project Path="App.Tests/App.Tests.csproj" />
</Solution>`)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 3 {
		t.Fatalf("got %d projects, want 3: %v", len(projects), projects)
	}

	app := byName(t, projects, "App")
	if !app.IsRunnable() {
		t.Error("App should be runnable")
	}
	if app.IsTest {
		t.Error("App should not be a test project")
	}
	if app.Tfm != "net9.0" {
		t.Errorf("App.Tfm = %q, want net9.0", app.Tfm)
	}

	if byName(t, projects, "Lib").IsRunnable() {
		t.Error("Lib should not be runnable")
	}

	tests := byName(t, projects, "App.Tests")
	if !tests.IsTest { // via Microsoft.NET.Test.Sdk reference
		t.Error("App.Tests should be a test project")
	}
	if tests.IsRunnable() {
		t.Error("App.Tests should not be runnable")
	}
}

func TestLoadProject_ParsesProjectReferenceDeps(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "App", "App.csproj")
	writeFile(t, path, `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net9.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <ProjectReference Include="..\Lib\Lib.csproj" />
    <ProjectReference Include="../Core/Core.csproj" />
  </ItemGroup>
</Project>`)

	app := LoadProject(path, tmp)
	if len(app.Deps) != 2 || app.Deps[0] != "Lib" || app.Deps[1] != "Core" {
		t.Fatalf("Deps = %v, want [Lib Core] (base names, backslashes normalized)", app.Deps)
	}
}

func TestDiscoverDotNet_ExcludeGlobsDropMatchingProjectsByNameOrPath(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "Bench", "App.Bench.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "samples", "Demo", "Demo.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "App.slnx"), `<Solution>
  <Project Path="App/App.csproj" />
  <Project Path="Bench/App.Bench.csproj" />
  <Project Path="samples/Demo/Demo.csproj" />
</Solution>`)

	kept := DiscoverDotNet(tmp, "", []string{"*.Bench", "samples/*"})

	// bench (by name) + demo (by path) dropped
	if len(kept) != 1 || kept[0].Name != "App" {
		t.Fatalf("got %v, want just App", kept)
	}
}

func TestIsExcluded_MatchesOnNameAndForwardSlashedPath(t *testing.T) {
	p := ProjectInfo{
		Name:       "Acme.Spike",
		RelPath:    filepath.Join("spikes", "Acme.Spike", "Acme.Spike.csproj"),
		FullPath:   "/r/spikes/Acme.Spike/Acme.Spike.csproj",
		OutputType: "Exe", Tfm: "net8.0",
	}

	if !IsExcluded(p, []string{"*Spike"}) {
		t.Error("should be excluded by name")
	}
	if !IsExcluded(p, []string{"spikes/*"}) {
		t.Error("should be excluded by path")
	}
	if IsExcluded(p, []string{"*.Demo"}) {
		t.Error("should not match *.Demo")
	}
	if IsExcluded(p, nil) {
		t.Error("nil exclude list should exclude nothing")
	}
}

func TestDiscoverDotNet_ParsesClassicSlnProjectLines(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "App.sln"), `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "App", "App\App.csproj", "{11111111-1111-1111-1111-111111111111}"
EndProject
Global
EndGlobal`)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1: %v", len(projects), projects)
	}
	if projects[0].Name != "App" {
		t.Errorf("Name = %q, want App", projects[0].Name)
	}
	if !projects[0].IsRunnable() {
		t.Error("App should be runnable")
	}
}

func TestDiscoverDotNet_TestProjectDetectedByNameConvention(t *testing.T) {
	tmp := t.TempDir()
	// A *Tests project with no test-sdk reference still classifies as a test.
	writeFile(t, filepath.Join(tmp, "Foo.Tests", "Foo.Tests.csproj"), libCsproj)
	writeFile(t, filepath.Join(tmp, "App.slnx"),
		`<Solution><Project Path="Foo.Tests/Foo.Tests.csproj" /></Solution>`)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 1 || !projects[0].IsTest {
		t.Fatalf("got %v, want a single test project", projects)
	}
}

func TestDiscoverDotNet_ScanSkipsBuildOutput(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net8.0"))
	// bin/obj artifacts that must be ignored
	writeFile(t, filepath.Join(tmp, "App", "bin", "Debug", "Ghost.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "App", "obj", "Ghost.csproj"), exeCsproj("net8.0"))

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 1 || projects[0].Name != "App" {
		t.Fatalf("got %v, want just App", projects)
	}
}

func TestDiscoverDotNet_ScanFindsFsprojAndVbproj(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "Fs", "Fs.fsproj"), exeCsproj("net9.0"))
	writeFile(t, filepath.Join(tmp, "Vb", "Vb.vbproj"), libCsproj)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 2 {
		t.Fatalf("got %v, want Fs and Vb", projects)
	}
	if !byName(t, projects, "Fs").IsRunnable() {
		t.Error("Fs should be runnable")
	}
}

func TestDiscoverDotNet_ConfiguredSolutionOverrideIsHonoured(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net8.0"))
	writeFile(t, filepath.Join(tmp, "Other", "Other.csproj"), libCsproj)
	writeFile(t, filepath.Join(tmp, "custom.slnx"),
		`<Solution><Project Path="App/App.csproj" /></Solution>`)

	// An explicit pin still SCOPES discovery — Other is on disk but out of scope.
	projects := DiscoverDotNet(tmp, "custom.slnx", nil)
	if len(projects) != 1 || projects[0].Name != "App" {
		t.Fatalf("got %v, want just App", projects)
	}
}

func TestDiscoverDotNet_ConfiguredSolutionThatIsMissingFallsBackToTheScan(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "App", "App.csproj"), exeCsproj("net8.0"))

	// A stale pin must not report an empty repo (`rig doctor` flags the pin).
	projects := DiscoverDotNet(tmp, "gone.slnx", nil)
	if len(projects) != 1 || projects[0].Name != "App" {
		t.Fatalf("got %v, want just App", projects)
	}
}

// A repo whose only ROOT-level solution is a test-only aggregate, with the real
// build solutions nested one level down (the shape that broke: rig picked the
// root solution and every non-test project vanished from `rig info`/`rig run`).
func TestDiscoverDotNet_TestOnlyRootSolutionDoesNotHideTheApp(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "ui", "src", "App", "App.csproj"), exeCsproj("net10.0"))
	writeFile(t, filepath.Join(tmp, "ui", "tests", "App.Tests", "App.Tests.csproj"), testCsproj)
	writeFile(t, filepath.Join(tmp, "ui", "Ui.slnx"), `<Solution>
  <Project Path="src/App/App.csproj" />
  <Project Path="tests/App.Tests/App.Tests.csproj" />
</Solution>`)
	writeFile(t, filepath.Join(tmp, "Repo.Tests.slnx"), `<Solution>
  <Project Path="ui/tests/App.Tests/App.Tests.csproj" />
</Solution>`)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 2 {
		t.Fatalf("got %v, want App and App.Tests", projects)
	}
	if !byName(t, projects, "App").IsRunnable() {
		t.Error("App should be discovered and runnable — it is only in the nested solution")
	}
}

// Several solutions at the root, each covering a slice: no single one is "the"
// solution, so discovery reports the whole repo rather than the alphabetically
// first slice.
func TestDiscoverDotNet_MultipleRootSolutionsDoNotPartitionTheRepo(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "Api", "Api.csproj"), exeCsproj("net9.0"))
	writeFile(t, filepath.Join(tmp, "Web", "Web.csproj"), exeCsproj("net9.0"))
	writeFile(t, filepath.Join(tmp, "A.slnx"),
		`<Solution><Project Path="Api/Api.csproj" /></Solution>`)
	writeFile(t, filepath.Join(tmp, "B.slnx"),
		`<Solution><Project Path="Web/Web.csproj" /></Solution>`)

	projects := DiscoverDotNet(tmp, "", nil)
	if len(projects) != 2 {
		t.Fatalf("got %v, want both Api and Web", projects)
	}
	byName(t, projects, "Api")
	byName(t, projects, "Web")
}

// ---- ports of the .NET rig's TestEnumerationTests (project-metadata level) ----
//
// The .NET rig classifies test CLASSES by reading assembly metadata
// (MetadataLoadContext over [TestClass]/[TestFixture]/[Fact] attributes). Go
// has no .NET metadata reader, so the equivalent classification surface here
// is the csproj-level test signals in LoadProject: IsTestProject /
// EnableMSTestRunner / a Microsoft.NET.Test.Sdk reference / the *Tests naming
// convention. The C# suite's real-assembly enumeration and cross-TFM
// MetadataLoadContext gate need built .NET assemblies and are not portable.

func loadTempProject(t *testing.T, name, csproj string) ProjectInfo {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, name, name+".csproj")
	writeFile(t, path, csproj)
	return LoadProject(path, tmp)
}

func TestLoadProject_DetectsMSTestViaEnableMSTestRunner(t *testing.T) {
	// EnableMSTestRunner is the MSTest-runner opt-in — a test project even
	// without the VSTest SDK package.
	p := loadTempProject(t, "Unit", `<Project Sdk="MSTest.Sdk">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <EnableMSTestRunner>true</EnableMSTestRunner>
  </PropertyGroup>
</Project>`)
	if !p.IsTest {
		t.Fatal("EnableMSTestRunner should classify as a test project")
	}
}

func TestLoadProject_DetectsTestViaIsTestProjectProperty(t *testing.T) {
	p := loadTempProject(t, "Unit", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <IsTestProject>true</IsTestProject>
  </PropertyGroup>
</Project>`)
	if !p.IsTest {
		t.Fatal("IsTestProject should classify as a test project")
	}
}

func TestLoadProject_DetectsNUnitAndXunitViaTestSdkReference(t *testing.T) {
	// NUnit and xUnit projects both carry the Microsoft.NET.Test.Sdk package —
	// the project-level signal shared by every VSTest framework.
	for _, framework := range []string{"NUnit", "xunit"} {
		p := loadTempProject(t, "Unit", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.NET.Test.Sdk" Version="18.0.0" />
    <PackageReference Include="`+framework+`" Version="4.0.0" />
  </ItemGroup>
</Project>`)
		if !p.IsTest {
			t.Fatalf("a %s project referencing Microsoft.NET.Test.Sdk should classify as a test project", framework)
		}
	}
}

func TestLoadProject_PlainProjectIsNotATestProject(t *testing.T) {
	// No test signals at all — an ordinary library with unrelated packages.
	p := loadTempProject(t, "Core", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
  </ItemGroup>
</Project>`)
	if p.IsTest {
		t.Fatal("a plain library must not classify as a test project")
	}
}

func TestLoadProject_TestsNameSuffixConventionClassifies(t *testing.T) {
	p := loadTempProject(t, "Core.Tests", libCsproj)
	if !p.IsTest {
		t.Fatal("the *Tests naming convention should classify as a test project")
	}
}

// A pin that names an existing solution scopes discovery to it — even when that
// solution lists no supported projects. Falling back to a repo-wide scan there
// would silently widen an explicitly scoped repo, which is the one thing a pin
// exists to prevent.
func TestDiscoverDotNetHonoursAPinThatYieldsNothing(t *testing.T) {
	root := t.TempDir()
	// An empty solution, plus a project it does not list.
	if err := os.WriteFile(filepath.Join(root, "Empty.slnx"), []byte("<Solution/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "Other", "Other.csproj")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("<Project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverDotNet(root, "Empty.slnx", nil); len(got) != 0 {
		t.Fatalf("pinned discovery returned %d project(s), want none — the pin was ignored", len(got))
	}
	// A pin naming a MISSING file still falls back, so the repo is not reported
	// as empty when the config is simply wrong (doctor flags that separately).
	if got := DiscoverDotNet(root, "NoSuch.slnx", nil); len(got) == 0 {
		t.Fatal("a pin naming a missing solution should fall back to the scan")
	}
}
