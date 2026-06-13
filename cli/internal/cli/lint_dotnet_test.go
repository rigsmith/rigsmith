package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdirT(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDotnetAnalyzerPackages_None(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "App.csproj"), `
<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
  </ItemGroup>
</Project>`)
	if got := dotnetAnalyzerPackages(root); len(got) != 0 {
		t.Errorf("expected no analyzers, got %v", got)
	}
}

func TestDotnetAnalyzerPackages_ProjectAndCentralProps(t *testing.T) {
	root := t.TempDir()
	// A PackageReference in a project file.
	writeFile(t, filepath.Join(root, "App.csproj"), `
<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Meziantou.Analyzer" Version="2.0.0" />
    <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
  </ItemGroup>
</Project>`)
	// A PackageVersion in central package management, in a nested dir.
	sub := filepath.Join(root, "src")
	mkdirT(t, sub)
	writeFile(t, filepath.Join(sub, "Directory.Packages.props"), `
<Project>
  <ItemGroup>
    <PackageVersion Include="SonarAnalyzer.CSharp" Version="9.0.0" />
  </ItemGroup>
</Project>`)
	// A bin/ copy must be ignored even though it mentions an analyzer.
	mkdirT(t, filepath.Join(root, "bin"))
	writeFile(t, filepath.Join(root, "bin", "Leftover.csproj"),
		`<Project><ItemGroup><PackageReference Include="StyleCop.Analyzers" Version="1.0.0" /></ItemGroup></Project>`)

	got := dotnetAnalyzerPackages(root)
	want := []string{"Meziantou.Analyzer", "SonarAnalyzer.CSharp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("analyzers = %v, want %v (bin/ should be skipped, results sorted+deduped)", got, want)
	}
}
