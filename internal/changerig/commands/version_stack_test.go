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
	if !strings.Contains(out, "not stamped (no version in the tree, or a stackspace member's manifest): Lib") {
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

// A `--no-stamp` release leaves the manifest behind; the next stamped run
// bumps from the version that shipped (the record), writes it into the tree,
// and reconciles the record away so it cannot go stale.
func TestVersionStampedRunBumpsFromTheRecordAndReconcilesIt(t *testing.T) {
	root := stampWorkspace(t, false)
	runVersion(t, "--no-stamp")
	state, err := versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil || state.Get("App") != "1.1.0" {
		t.Fatalf("after --no-stamp, versions.json = %+v, %v", state, err)
	}
	if got := mustRead(t, filepath.Join(root, "App", "App.csproj")); !strings.Contains(got, "<Version>1.0.0</Version>") {
		t.Fatalf("--no-stamp wrote the manifest:\n%s", got)
	}

	writeFileAt(t, filepath.Join(root, ".changeset", "app-fix.md"), "---\n\"App\": patch\n---\n\nApp fixed\n")
	out := runVersion(t)
	if strings.Contains(out, "not stamped") {
		t.Fatalf("a stamped run reported something unstamped:\n%s", out)
	}
	if got := mustRead(t, filepath.Join(root, "App", "App.csproj")); !strings.Contains(got, "<Version>1.1.1</Version>") {
		t.Fatalf("the stamped run did not bump from the recorded 1.1.0:\n%s", got)
	}
	state, err = versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Get("App") != "" {
		t.Fatalf("the record for App was not reconciled away: %+v", state)
	}
	if state.Get("Lib") != "2.0.1" {
		t.Fatalf("Lib's record (untouched by this run) was lost: %+v", state)
	}
}

// A package of the stackspace's own that lives at the root shares the root
// CHANGELOG.md with the members' sections. Both write sections of it, in
// whichever order the plan visits them, and neither lands under the other's
// heading.
func TestVersionRootPackageSharesTheRootChangelogWithMembers(t *testing.T) {
	for _, first := range []string{"Root", "Lib"} {
		t.Run(first+" first", func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main"}`)
			writeFileAt(t, filepath.Join(root, "Root.csproj"), "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>\n")
			writeFileAt(t, filepath.Join(root, "lib", "src", "Lib", "Lib.csproj"), "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <Version>2.0.0</Version>\n  </PropertyGroup>\n</Project>\n")
			writeFileAt(t, filepath.Join(root, "rig.stack.jsonc"), `{ "repos": { "lib": { "upstream": "h/acme/lib", "fork": "h/you/lib" } } }`)
			// Two runs, one package each, so the order the sections are
			// written in is the order under test.
			second := "Lib"
			if first == "Lib" {
				second = "Root"
			}
			t.Chdir(root)
			for _, name := range []string{first, second} {
				writeFileAt(t, filepath.Join(root, ".changeset", strings.ToLower(name)+".md"), "---\n\""+name+"\": patch\n---\n\n"+name+" changed\n")
				runVersion(t)
			}
			log := mustRead(t, filepath.Join(root, "CHANGELOG.md"))
			rootAt := strings.Index(log, "# Root\n")
			libAt := strings.Index(log, "# Lib\n")
			if rootAt < 0 || libAt < 0 {
				t.Fatalf("both sections expected in the root changelog:\n%s", log)
			}
			if strings.Count(log, "# Root\n") != 1 || strings.Count(log, "# Lib\n") != 1 {
				t.Fatalf("a section was written twice:\n%s", log)
			}
			// Each package's entry sits inside its own section: between its
			// heading and the next top-level heading.
			section := func(from int) string {
				rest := log[from+1:]
				if next := strings.Index(rest, "\n# "); next >= 0 {
					return rest[:next]
				}
				return rest
			}
			if s := section(rootAt); !strings.Contains(s, "## 1.0.1") || !strings.Contains(s, "Root changed") || strings.Contains(s, "Lib changed") {
				t.Fatalf("Root's section:\n%s\n\nwhole file:\n%s", s, log)
			}
			if s := section(libAt); !strings.Contains(s, "## 2.0.1") || !strings.Contains(s, "Lib changed") || strings.Contains(s, "Root changed") {
				t.Fatalf("Lib's section:\n%s\n\nwhole file:\n%s", s, log)
			}
			if got := mustRead(t, filepath.Join(root, "Root.csproj")); !strings.Contains(got, "<Version>1.0.1</Version>") {
				t.Fatalf("the root package was not stamped:\n%s", got)
			}
		})
	}
}

// A package whose version is computed at build time (MinVer here) has no
// number in the tree to stamp: the release records it, never inserts a
// <Version> into the project, and the record is the current version next
// time.
func TestVersionComputedVersionIsRecordedNeverStamped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main"}`)
	project := "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <ItemGroup>\n    <PackageReference Include=\"MinVer\" Version=\"5.0.0\" PrivateAssets=\"All\" />\n  </ItemGroup>\n</Project>\n"
	writeFileAt(t, filepath.Join(root, "Tool", "Tool.csproj"), project)
	writeFileAt(t, filepath.Join(root, ".changeset", "tool.md"), "---\n\"Tool\": minor\n---\n\nTool grew\n")
	t.Chdir(root)

	out := runVersion(t)
	if !strings.Contains(out, "not stamped (no version in the tree, or a stackspace member's manifest): Tool") {
		t.Fatalf("output:\n%s", out)
	}
	if got := mustRead(t, filepath.Join(root, "Tool", "Tool.csproj")); got != project {
		t.Fatalf("the project was written:\n%s", got)
	}
	state, err := versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil || state.Get("Tool") != "0.1.0" {
		t.Fatalf("versions.json = %+v, %v; want Tool at 0.1.0 (a minor bump from nothing)", state, err)
	}

	// Next release: the record is the current version, and stays a record.
	writeFileAt(t, filepath.Join(root, ".changeset", "tool2.md"), "---\n\"Tool\": patch\n---\n\nTool fixed\n")
	runVersion(t)
	state, err = versionstate.Read(filepath.Join(root, ".changeset"))
	if err != nil || state.Get("Tool") != "0.1.1" {
		t.Fatalf("second release: versions.json = %+v, %v", state, err)
	}
	if got := mustRead(t, filepath.Join(root, "Tool", "Tool.csproj")); got != project {
		t.Fatalf("the second release wrote the project:\n%s", got)
	}
}
