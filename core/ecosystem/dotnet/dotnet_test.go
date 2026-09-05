package dotnet

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()

	// Lib with an inline <Version> and a PackageId override.
	writeFile(t, filepath.Join(root, "src", "Lib", "Lib.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <PackageId>Acme.Lib</PackageId>
    <Version>1.2.3</Version>
  </PropertyGroup>
</Project>`)

	// App referencing Lib, version inherited from a shared props file.
	writeFile(t, filepath.Join(root, "src", "App", "App.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <VersionPrefix>9.9.9</VersionPrefix>
  </PropertyGroup>
  <ItemGroup>
    <ProjectReference Include="..\Lib\Lib.csproj" />
  </ItemGroup>
</Project>`)

	// Shared props that App inherits from (App has its own inline VersionPrefix, so
	// it should NOT inherit; we add a third project to exercise the props path).
	writeFile(t, filepath.Join(root, "Directory.Build.props"), `<Project>
  <PropertyGroup>
    <Version>5.0.0</Version>
  </PropertyGroup>
</Project>`)

	writeFile(t, filepath.Join(root, "src", "Worker", "Worker.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Lib\Lib.csproj" />
  </ItemGroup>
</Project>`)

	a := New()
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]plugin.Package{}
	for _, p := range resp.Packages {
		byName[p.Name] = p
	}

	if len(byName) != 3 {
		t.Fatalf("expected 3 packages, got %d: %v", len(byName), keys(byName))
	}

	// Lib: PackageId is the name, inline version, no VersionFile.
	lib := byName["Acme.Lib"]
	if lib.Version != "1.2.3" {
		t.Errorf("Lib version = %q, want 1.2.3", lib.Version)
	}
	if lib.VersionFile != "" {
		t.Errorf("Lib VersionFile = %q, want empty (inline)", lib.VersionFile)
	}

	// App: filename name, inline VersionPrefix, ProjectReference to Lib (rangeless).
	app := byName["App"]
	if app.Version != "9.9.9" {
		t.Errorf("App version = %q, want 9.9.9", app.Version)
	}
	if len(app.Dependencies) != 1 || app.Dependencies[0].Name != "Lib" || app.Dependencies[0].Range != "" {
		t.Errorf("App deps = %+v, want [{Lib normal ''}]", app.Dependencies)
	}

	// Worker: no inline version -> inherits from shared props, VersionFile set.
	worker := byName["Worker"]
	if worker.Version != "5.0.0" {
		t.Errorf("Worker version = %q, want 5.0.0 (from props)", worker.Version)
	}
	if worker.VersionFile != "Directory.Build.props" {
		t.Errorf("Worker VersionFile = %q, want Directory.Build.props", worker.VersionFile)
	}
}

func TestSetVersionInline(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Lib.csproj")
	writeFile(t, manifest, `<Project>
  <PropertyGroup>
    <Version>1.2.3</Version>
  </PropertyGroup>
</Project>`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "Lib.csproj"},
		NewVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if want := "<Version>2.0.0</Version>"; !strings.Contains(string(got), want) {
		t.Errorf("manifest after set = %q, want it to contain %q", got, want)
	}
}

// TestDiscoverSkipsProjectWithNoVersion checks that a csproj with no inline
// <Version>/<VersionPrefix> and no ancestor Directory.Build.props version is
// skipped entirely (matches the C# resolver returning null).
func TestDiscoverSkipsProjectWithNoVersion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`)

	a := New()
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 0 {
		t.Errorf("expected no packages, got %+v", resp.Packages)
	}
}

// A project whose version is computed at build time — MinVer from git tags, a
// CI stamp — carries no number in the tree, but it is a package all the same:
// it comes back with an empty Version rather than not at all. A project that is
// merely unversioned (no package, no MinVer) stays out, and IsPackable false
// keeps one out whatever else it declares.
func TestDiscoverIncludesPackableProjectsWithComputedVersions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "mermaider", "Directory.Build.props"), `<Project>
  <PropertyGroup>
    <MinVerMinimumMajorMinor>0.12</MinVerMinimumMajorMinor>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "mermaider", "src", "Mermaider", "Mermaider.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "live", "src", "LiveMarkdown", "LiveMarkdown.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsPackable>true</IsPackable>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "live", "src", "Ref", "Ref.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="MinVer" Version="5.0.0" PrivateAssets="all" />
  </ItemGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "live", "tests", "Ref.Tests", "Ref.Tests.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsPackable>false</IsPackable>
    <PackageId>Never.Packed</PackageId>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "tools", "Tool", "Tool.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
  </PropertyGroup>
</Project>`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range resp.Packages {
		got[p.Name] = p.Version
	}
	for _, want := range []string{"Mermaider", "LiveMarkdown", "Ref"} {
		if v, ok := got[want]; !ok || v != "" {
			t.Errorf("%s: present=%v version=%q; want present with no version", want, ok, v)
		}
	}
	for _, unwanted := range []string{"Ref.Tests", "Never.Packed", "Tool"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%s discovered as a package", unwanted)
		}
	}
}

// Two shapes of "this project packs" that discovery used to miss. A shared
// props file sets IsPackable false for everything and true again in a later
// PropertyGroup under a Condition — the first IsPackable found is not the last
// word, and conditions are not evaluated, so any true in the chain counts. And
// under Central Package Management MinVer is declared once, as a
// GlobalPackageReference in Directory.Packages.props, with no csproj naming it.
// An IsPackable false with no true anywhere still keeps a project out.
func TestDiscoverPackableFromConditionalPropsAndGlobalMinVer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "src", "AInline", "AInline.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsPackable>true</IsPackable>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "b", "Directory.Build.props"), `<Project>
  <PropertyGroup>
    <IsPackable>false</IsPackable>
  </PropertyGroup>
  <PropertyGroup Condition="$(MSBuildProjectDirectory.Replace('\','/').Contains('/src/'))">
    <IsPackable>true</IsPackable>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "b", "src", "BCond", "BCond.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "c", "Directory.Packages.props"), `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <GlobalPackageReference Include="MinVer" Version="7.0.0" PrivateAssets="All" />
  </ItemGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "c", "src", "CMinVer", "CMinVer.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "d", "Directory.Build.props"), `<Project>
  <PropertyGroup>
    <IsPackable>false</IsPackable>
  </PropertyGroup>
</Project>`)
	writeFile(t, filepath.Join(root, "d", "src", "DNever", "DNever.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <PackageId>Never.Packed</PackageId>
  </PropertyGroup>
</Project>`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range resp.Packages {
		got[p.Name] = p.Version
	}
	for _, want := range []string{"AInline", "BCond", "CMinVer"} {
		if v, ok := got[want]; !ok || v != "" {
			t.Errorf("%s: present=%v version=%q; want present with no version", want, ok, v)
		}
	}
	for _, unwanted := range []string{"DNever", "Never.Packed"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%s discovered as a package despite IsPackable false and no true anywhere", unwanted)
		}
	}
}

// packable's IsPackable rule, one text at a time: every IsPackable inside a
// PropertyGroup is read, a true anywhere wins, a false with no true excludes,
// and one outside any PropertyGroup (item metadata) is not a property at all.
func TestPackableReadsEveryIsPackable(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"false then conditional true", `<Project>
  <PropertyGroup><IsPackable>false</IsPackable></PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)'=='Release'"><IsPackable>true</IsPackable></PropertyGroup>
</Project>`, true},
		{"true then false", `<Project>
  <PropertyGroup><IsPackable>true</IsPackable></PropertyGroup>
  <PropertyGroup><IsPackable>false</IsPackable></PropertyGroup>
</Project>`, true},
		{"false only, with PackageId", `<Project>
  <PropertyGroup><IsPackable>false</IsPackable><PackageId>X</PackageId></PropertyGroup>
</Project>`, false},
		{"true outside a PropertyGroup", `<Project>
  <ItemGroup><Thing Include="x"><IsPackable>true</IsPackable></Thing></ItemGroup>
</Project>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "P.csproj")
			if got := packable(path, tc.text); got != tc.want {
				t.Errorf("packable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetVersionWritesPrefixLeavesSuffix(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "P.csproj")
	writeFile(t, manifest, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><VersionPrefix>1.0.0</VersionPrefix><VersionSuffix>beta</VersionSuffix></PropertyGroup></Project>`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "P.csproj"},
		NewVersion: "1.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if want := "<VersionPrefix>1.1.0</VersionPrefix>"; !strings.Contains(string(got), want) {
		t.Errorf("manifest after set = %q, want it to contain %q", got, want)
	}
	if want := "<VersionSuffix>beta</VersionSuffix>"; !strings.Contains(string(got), want) {
		t.Errorf("manifest after set = %q, want suffix untouched: %q", got, want)
	}
}

func TestSetVersionInsertsWhenAbsent(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "App.csproj")
	writeFile(t, manifest, `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "App.csproj"},
		NewVersion: "3.1.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if want := "<Version>3.1.4</Version>"; !strings.Contains(string(got), want) {
		t.Errorf("manifest after insert = %q, want it to contain %q", got, want)
	}
}

// TestSetVersionRewritesOnlyFirstVersion pins the fix where SetVersion rewrote
// EVERY <Version> element. A csproj with two PropertyGroups, each carrying its
// own <Version> under a different Condition, must have only the FIRST <Version>
// changed; the second is left untouched.
func TestSetVersionRewritesOnlyFirstVersion(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Multi.csproj")
	writeFile(t, manifest, `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup Condition="'$(Configuration)'=='Debug'">
    <Version>1.0.0</Version>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)'=='Release'">
    <Version>3.0.0</Version>
  </PropertyGroup>
</Project>`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "Multi.csproj"},
		NewVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, manifest))
	if !strings.Contains(got, "<Version>2.0.0</Version>") {
		t.Errorf("first <Version> not updated: %q", got)
	}
	if !strings.Contains(got, "<Version>3.0.0</Version>") {
		t.Errorf("second <Version> must stay 3.0.0: %q", got)
	}
	if strings.Contains(got, "<Version>1.0.0</Version>") {
		t.Errorf("first <Version> should no longer be 1.0.0: %q", got)
	}
}

// itemMetadataProject mirrors the shape that exposed the bug: a custom item whose
// metadata is spelled with the same element syntax as a property, declared BEFORE
// the project's own version. Avalite's icon packs look exactly like this.
const itemMetadataProject = `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <IconPack Include="lucide">
      <Name>Lucide</Name>
      <Version>0.2.0</Version>
    </IconPack>
  </ItemGroup>
  <PropertyGroup>
    <Version>1.0.0</Version>
  </PropertyGroup>
</Project>`

func TestDiscoverIgnoresItemMetadataVersion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Lib.csproj"), itemMetadataProject)

	a := New()
	got, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]plugin.Package{}
	for _, p := range got.Packages {
		byName[p.Name] = p
	}
	pkg, ok := byName["Lib"]
	if !ok {
		t.Fatalf("Lib not discovered: %v", keys(byName))
	}
	// 0.2.0 is the icon pack's version, not the project's.
	if pkg.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (item metadata must not be read as the project version)", pkg.Version)
	}
}

func TestSetVersionLeavesItemMetadataAlone(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Lib.csproj")
	writeFile(t, manifest, itemMetadataProject)

	a := New()
	if err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "Lib.csproj"},
		NewVersion: "2.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, manifest))
	if !strings.Contains(got, "<Version>2.0.0</Version>") {
		t.Errorf("project <Version> not bumped: %q", got)
	}
	// The regression: the bump used to land on the FIRST <Version> in the file,
	// silently rewriting the icon pack's version and leaving the project at 1.0.0.
	if !strings.Contains(got, "<Version>0.2.0</Version>") {
		t.Errorf("icon pack metadata was rewritten: %q", got)
	}
	if strings.Contains(got, "<Version>1.0.0</Version>") {
		t.Errorf("project version should no longer be 1.0.0: %q", got)
	}
}

// A project whose only <Version> is item metadata has no project version at all;
// the bump must be INSERTED into a PropertyGroup rather than hijacking the item.
func TestSetVersionInsertsWhenOnlyItemMetadataVersionExists(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Lib.csproj")
	writeFile(t, manifest, `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <IconPack Include="lucide">
      <Version>0.2.0</Version>
    </IconPack>
  </ItemGroup>
</Project>`)

	a := New()
	if err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "Lib.csproj"},
		NewVersion: "2.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, manifest))
	if !strings.Contains(got, "<Version>2.0.0</Version>") {
		t.Errorf("version not inserted: %q", got)
	}
	if !strings.Contains(got, "<Version>0.2.0</Version>") {
		t.Errorf("icon pack metadata was rewritten: %q", got)
	}
	// Inserted into the PropertyGroup, not the ItemGroup.
	if strings.Index(got, "<Version>2.0.0</Version>") > strings.Index(got, "<ItemGroup>") {
		t.Errorf("version inserted outside the PropertyGroup: %q", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func keys(m map[string]plugin.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPublishPrivateSkipped checks that a private project is skipped before any
// `dotnet` invocation (hermetic — no toolchain required).
func TestPublishPrivateSkipped(t *testing.T) {
	a := New()
	resp, err := a.Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: t.TempDir(),
		Package:  plugin.Package{Name: "Acme.Lib", Version: "1.0.0", Dir: ".", ManifestPath: "Acme.Lib.csproj", Private: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Published || !resp.Skipped || resp.Message != "private" {
		t.Errorf("private publish = %+v, want {Skipped private}", resp)
	}
}

// TestArtifactsPrivateSkipped checks a private project is skipped before any
// `dotnet pack` (hermetic — no toolchain required).
func TestArtifactsPrivateSkipped(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		Package:   plugin.Package{Name: "Acme.Lib", Version: "1.0.0", Dir: ".", ManifestPath: "Acme.Lib.csproj", Private: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || !resp.Skipped || resp.Message != "private" {
		t.Errorf("private artifacts = %+v, want {Skipped private}", resp)
	}
}

// TestArtifactsDryRun reports intent without running dotnet (hermetic).
func TestArtifactsDryRun(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		DryRun:    true,
		Package:   plugin.Package{Name: "Acme.Lib", Version: "1.0.0", Dir: ".", ManifestPath: "Acme.Lib.csproj"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || resp.Skipped || resp.Message != "dry-run: would dotnet pack Acme.Lib@1.0.0" {
		t.Errorf("dry-run artifacts = %+v, want a would-dotnet-pack message", resp)
	}
}

// A pack carries the release's version on the command line, so a project
// whose version is computed at build time packs under the name the release
// then looks for.
func TestPackPassesTheResolvedVersion(t *testing.T) {
	restore := packRunner
	t.Cleanup(func() { packRunner = restore })
	var got [][]string
	packRunner = func(ctx context.Context, dir, name string, args ...string) (string, string, error) {
		got = append(got, append([]string{name}, args...))
		return "", "", nil
	}
	out := t.TempDir()
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: out,
		Package:   plugin.Package{Name: "Mermaider", Version: "0.12.3", Dir: "src/Mermaider", ManifestPath: "src/Mermaider/Mermaider.csproj"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0][0] != "dotnet" || got[0][1] != "pack" || !slices.Contains(got[0], "-p:Version=0.12.3") {
		t.Fatalf("pack command = %v, want dotnet pack … -p:Version=0.12.3", got)
	}
	if want := filepath.Join(out, "Mermaider.0.12.3.nupkg"); len(resp.Artifacts) != 1 || resp.Artifacts[0].Path != want {
		t.Fatalf("artifacts = %+v, want the nupkg the version names", resp.Artifacts)
	}
	if args := packArgs("A.csproj", "dist", ""); slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-p:Version=") }) {
		t.Errorf("no version, yet -p:Version passed: %v", args)
	}
}

// TestInfoAdvertisesArtifacts locks the .NET adapter's artifacts capability.
func TestInfoAdvertisesArtifacts(t *testing.T) {
	found := false
	for _, c := range New().Info().Capabilities {
		if c == plugin.MethodArtifacts {
			found = true
		}
	}
	if !found {
		t.Error("dotnet Info() should advertise MethodArtifacts")
	}
}

// TestRunCmdSurfacesStdoutOnFailure pins the wiring end-to-end: a command that
// fails talking only to stdout still produces an error that says why.
//
// The sentinel is split across printf's format and its argument, so the
// assembled text exists only in the command's stdout and never in the argv the
// error echoes back. Asserting on a string that also appears in the command
// line would pass whether or not the output was captured — which is precisely
// the bug under test.
func TestRunCmdSurfacesStdoutOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell to write to a chosen stream")
	}
	const sentinel = "error: 403 (Forbidden)."
	const script = `printf 'err%s\n' 'or: 403 (Forbidden).'; exit 1`
	if strings.Contains(script, sentinel) {
		t.Fatal("the sentinel must not appear in the command, or this test proves nothing")
	}

	_, _, err := runCmd(context.Background(), "", "sh", "-c", script)
	if err == nil {
		t.Fatal("want an error from a non-zero exit")
	}
	if !strings.Contains(err.Error(), sentinel) {
		t.Errorf("error = %q, want it to carry the stdout diagnosis %q", err, sentinel)
	}
}

// Detect gates whether rig's .NET discovery runs at all, so a false negative
// here hides the whole repo rather than one project.
func TestDetectFindsNonCSharpAndVendoredProjects(t *testing.T) {
	cases := []struct {
		name string
		rel  string
	}{
		{"F# only", "src/App/App.fsproj"},
		{"VB only", "src/App/App.vbproj"},
		// A vendored .csproj is a first-class build input in .NET — solutions
		// routinely list them — unlike a Go vendor tree.
		{"vendored only", "vendor/Lib/Lib.csproj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("<Project/>"), 0o644); err != nil {
				t.Fatal(err)
			}
			ok, err := (&Adapter{}).Detect(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatalf("Detect did not recognise %s as a .NET repo", tc.rel)
			}
		})
	}
}

// Build output still holds copies of project files and must not count.
func TestDetectIgnoresBuildOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "App", "obj", "Debug", "App.csproj")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<Project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := (&Adapter{}).Detect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a project file under obj/ was treated as a real project")
	}
}
