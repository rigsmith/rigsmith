package cmdtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `version --independent` writes each package's version inline into its own
// .csproj, overriding the shared Directory.Build.props (which is left alone),
// so packages that share a version file can move on their own changesets.
func TestVersionIndependentWritesInline(t *testing.T) {
	dir := tempDir(t)
	props := filepath.Join(dir, "Directory.Build.props")
	writeFile(t, props, "<Project>\n  <PropertyGroup>\n    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>\n")
	csproj := func(name string) string { return filepath.Join(dir, name, name+".csproj") }
	for _, name := range []string{"A", "B"} {
		writeFile(t, csproj(name),
			"<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n  </PropertyGroup>\n</Project>\n")
	}
	initChangesets(t, dir)
	writeChangeset(t, dir, "a-change", "A", "minor", "a feature")
	writeChangeset(t, dir, "b-change", "B", "patch", "a fix")

	code, out := runChangerig(t, dir, "version", "--independent")
	assertExitZero(t, code, out)

	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(csproj("A")); !strings.Contains(got, "<Version>1.1.0</Version>") {
		t.Errorf("A.csproj should have inline <Version>1.1.0</Version>:\n%s", got)
	}
	if got := read(csproj("B")); !strings.Contains(got, "<Version>1.0.1</Version>") {
		t.Errorf("B.csproj should have inline <Version>1.0.1</Version>:\n%s", got)
	}
	if got := read(props); !strings.Contains(got, "<Version>1.0.0</Version>") {
		t.Errorf("Directory.Build.props should be untouched (still 1.0.0):\n%s", got)
	}
}
