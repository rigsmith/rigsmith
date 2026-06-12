package dotnet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/core/plugin"
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
