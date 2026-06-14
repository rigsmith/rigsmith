package cmdtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sharedPropsWorkspace materializes two csproj packages (A, B) that inherit
// their version from a root Directory.Build.props, with a minor changeset on A
// and a patch changeset on B. Returns the props path and a csproj path lookup.
func sharedPropsWorkspace(t *testing.T, dir string) (string, func(string) string) {
	t.Helper()
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
	return props, csproj
}

// `version --independent` writes each package's version inline into its own
// .csproj, overriding the shared Directory.Build.props (which is left alone),
// so packages that share a version file can move on their own changesets.
func TestVersionIndependentWritesInline(t *testing.T) {
	dir := tempDir(t)
	props, csproj := sharedPropsWorkspace(t, dir)

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

// Ported from UpdateCsProjectsVersionAsync_WritesASharedPropsFileOnce_ForALockstepGroup:
// a default (lockstep) `version` run over a shared-props pair coordinates both
// to the highest bump and lands exactly one <Version> in the props — no
// duplicate element from the second member's write — while the csproj files
// gain no inline version.
func TestVersionLockstepWritesSharedPropsOnce(t *testing.T) {
	dir := tempDir(t)
	props, csproj := sharedPropsWorkspace(t, dir)

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	got := readFile(t, props)
	if n := strings.Count(got, "<Version>1.1.0</Version>"); n != 1 {
		t.Errorf("props should contain <Version>1.1.0</Version> exactly once, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "<Version>"); n != 1 {
		t.Errorf("props should contain a single <Version> element, got %d:\n%s", n, got)
	}
	for _, name := range []string{"A", "B"} {
		if c := readFile(t, csproj(name)); strings.Contains(c, "<Version>") {
			t.Errorf("%s.csproj should not gain an inline version in lockstep mode:\n%s", name, c)
		}
	}
}
