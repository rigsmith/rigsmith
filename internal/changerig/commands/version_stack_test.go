package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/versionstate"
)

// stampWorkspace lays out a repo with a changeset config, one package at the
// root and one under a directory, each a .NET project with an inline version,
// plus a minor changeset for each. stack, when set, makes the directory a
// stackspace member.
func stampWorkspace(t *testing.T, stack bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main"}`)
	writeFileAt(t, filepath.Join(root, "App", "App.csproj"), "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>\n")
	writeFileAt(t, filepath.Join(root, "lib", "src", "Lib", "Lib.csproj"), "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <Version>2.0.0</Version>\n  </PropertyGroup>\n</Project>\n")
	writeFileAt(t, filepath.Join(root, ".changeset", "app-grows.md"), "---\n\"App\": minor\n---\n\nApp grew\n")
	writeFileAt(t, filepath.Join(root, ".changeset", "lib-fix.md"), "---\n\"Lib\": patch\n---\n\nLib fixed\n")
	if stack {
		writeFileAt(t, filepath.Join(root, "rig.stack.jsonc"), `{ "repos": { "lib": { "upstream": "h/acme/lib", "fork": "h/you/lib" } } }`)
	}
	t.Chdir(root)
	return root
}

func runVersion(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewVersionCmd()
	cmd.SetContext(context.Background())
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"--yes"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// In a stackspace a member's manifest is its upstream's: the version is
// computed, recorded beside the changesets and used as the current version
// next time, and the member's notes go to the root changelog — while the
// stackspace's own package is stamped as ever.
func TestVersionInAStackspaceDoesNotStampMembers(t *testing.T) {
	root := stampWorkspace(t, true)
	out := runVersion(t)
	if !strings.Contains(out, "not stamped (their manifests belong to stackspace members): Lib") {
		t.Fatalf("output does not say what was not stamped:\n%s", out)
	}
	if got := mustRead(t, filepath.Join(root, "lib", "src", "Lib", "Lib.csproj")); !strings.Contains(got, "<Version>2.0.0</Version>") {
		t.Fatalf("the member's manifest was written:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(root, "App", "App.csproj")); !strings.Contains(got, "<Version>1.1.0</Version>") {
		t.Fatalf("the stackspace's own package was not stamped:\n%s", got)
	}
	state, err := versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil || state.Get("Lib") != "2.0.1" || state.Get("App") != "" {
		t.Fatalf("versions.json = %+v, %v; want Lib 2.0.1 recorded and App not", state, err)
	}
	if _, err := os.Stat(filepath.Join(root, "lib", "src", "Lib", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Fatal("a changelog was written inside the member")
	}
	rootLog := mustRead(t, filepath.Join(root, "CHANGELOG.md"))
	if !strings.Contains(rootLog, "# Lib\n") || !strings.Contains(rootLog, "## 2.0.1") || !strings.Contains(rootLog, "Lib fixed") {
		t.Fatalf("the member's notes are not a section of the root changelog:\n%s", rootLog)
	}
	if got := mustRead(t, filepath.Join(root, "App", "CHANGELOG.md")); !strings.Contains(got, "## 1.1.0") {
		t.Fatalf("the root package's own changelog:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".changeset", "lib-fix.md")); !os.IsNotExist(err) {
		t.Fatal("the member's changeset was not consumed")
	}

	// The recorded version is the current one from here on.
	ws, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if p.Name == "Lib" && p.Version != "2.0.1" {
			t.Fatalf("Lib discovered at %q, want the recorded 2.0.1", p.Version)
		}
	}
}

// --no-stamp outside a stackspace: nothing in the tree changes but the
// changelogs, and the numbers are recorded.
func TestVersionNoStampRecordsInsteadOfWriting(t *testing.T) {
	root := stampWorkspace(t, false)
	out := runVersion(t, "--no-stamp")
	if !strings.Contains(out, "not stamped (--no-stamp): App, Lib") {
		t.Fatalf("output:\n%s", out)
	}
	for _, f := range []string{filepath.Join("App", "App.csproj"), filepath.Join("lib", "src", "Lib", "Lib.csproj")} {
		if got := mustRead(t, filepath.Join(root, f)); strings.Contains(got, "1.1.0") || strings.Contains(got, "2.0.1") {
			t.Fatalf("%s was stamped:\n%s", f, got)
		}
	}
	state, err := versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil || state.Get("App") != "1.1.0" || state.Get("Lib") != "2.0.1" {
		t.Fatalf("versions.json = %+v, %v", state, err)
	}
	if got := mustRead(t, filepath.Join(root, "lib", "src", "Lib", "CHANGELOG.md")); !strings.Contains(got, "## 2.0.1") {
		t.Fatalf("Lib's changelog stays beside it outside a stackspace:\n%s", got)
	}
}

// versioning.stamp: false in the config is the same as the flag on every run.
func TestVersionStampConfigOff(t *testing.T) {
	root := stampWorkspace(t, false)
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main", "versioning": {"stamp": false}}`)
	out := runVersion(t)
	if !strings.Contains(out, "not stamped (versioning.stamp is off)") {
		t.Fatalf("output:\n%s", out)
	}
	if got := mustRead(t, filepath.Join(root, "App", "App.csproj")); !strings.Contains(got, "<Version>1.0.0</Version>") {
		t.Fatalf("App was stamped:\n%s", got)
	}
}
