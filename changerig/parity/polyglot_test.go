package parity

// The cross-ecosystem polyglot cascade — the north-star scenario no other
// implementation can run: one changeset on a C# library releases a fixed group
// spanning all four ecosystems (dotnet, node, go, cargo), each manifest written
// in its native format, and the release cascades onward into an npm dependent
// of a group member (range rewritten + "Updated dependencies" changelog).
//
// There is no external oracle for a mixed-ecosystem repo. The goldens in
// core/testdata/parity/polyglot/ are SELF-AUTHORED, justified piecewise by the
// rest of the corpus: the version math and cascade semantics are Node-verified
// (fixed-group, fixed-group-dependent-cascade), the dotnet write-back is
// C#-cross-checked (TestDotnetCrossOracle), and the changelog format is pinned
// by the Node goldens.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPolyglotParity(t *testing.T) {
	dir := t.TempDir()

	// dotnet: src/Demo.Core (the changeset target).
	mkdirAll(t, filepath.Join(dir, "src", "Demo.Core"))
	writeFile(t, filepath.Join(dir, "src", "Demo.Core", "Demo.Core.csproj"),
		"<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>\n")

	// node: an npm workspace with a fixed-group member and a dependent of it.
	writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(dir, "package-lock.json"), "{}")
	mkdirAll(t, filepath.Join(dir, "packages", "demo-client"))
	writeFile(t, filepath.Join(dir, "packages", "demo-client", "package.json"), `{ "name": "demo-client", "version": "1.0.0" }`)
	mkdirAll(t, filepath.Join(dir, "packages", "demo-app"))
	writeFile(t, filepath.Join(dir, "packages", "demo-app", "package.json"), `{ "name": "demo-app", "version": "1.0.0", "dependencies": { "demo-client": "1.0.0" } }`)

	// go: a module with the inline rigsmith:version annotation.
	mkdirAll(t, filepath.Join(dir, "go-lib"))
	writeFile(t, filepath.Join(dir, "go-lib", "go.mod"), "module demo.example/lib // rigsmith:version 1.0.0\n\ngo 1.26\n")

	// cargo: a single crate.
	mkdirAll(t, filepath.Join(dir, "rust", "demo-rs", "src"))
	writeFile(t, filepath.Join(dir, "rust", "demo-rs", "Cargo.toml"), "[package]\nname = \"demo-rs\"\nversion = \"1.0.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(dir, "rust", "demo-rs", "src", "lib.rs"), "pub fn x() {}\n")

	mkdirAll(t, filepath.Join(dir, ".changeset"))
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "fixed": [["Demo.Core", "demo-client", "demo.example/lib", "demo-rs"]] }`)
	writeFile(t, filepath.Join(dir, ".changeset", "c.md"), "---\n\"Demo.Core\": minor\n---\n\nCross-ecosystem feature")

	runVersion(t, dir)

	// Every fixed member lands on 1.1.0, written back in its native format.
	if got := matchOne(t, readFile(t, filepath.Join(dir, "src", "Demo.Core", "Demo.Core.csproj")), `<Version>([^<]*)</Version>`); got != "1.1.0" {
		t.Errorf("Demo.Core csproj version = %q, want 1.1.0", got)
	}
	if got := readNodeVersion(t, dir, "demo-client"); got != "1.1.0" {
		t.Errorf("demo-client version = %q, want 1.1.0", got)
	}
	if got := matchOne(t, readFile(t, filepath.Join(dir, "go-lib", "go.mod")), `rigsmith:version[ \t]+(\S+)`); got != "1.1.0" {
		t.Errorf("go.mod rigsmith:version = %q, want 1.1.0", got)
	}
	if got := matchOne(t, readFile(t, filepath.Join(dir, "rust", "demo-rs", "Cargo.toml")), `version = "([^"]*)"`); got != "1.1.0" {
		t.Errorf("Cargo.toml version = %q, want 1.1.0", got)
	}

	// The cascade continues past the group: demo-app (exact dep on demo-client)
	// patch-bumps with its range rewritten — Node-verified semantics
	// (fixed-group-dependent-cascade), here crossing an ecosystem boundary in
	// the same plan.
	if got := readNodeVersion(t, dir, "demo-app"); got != "1.0.1" {
		t.Errorf("demo-app version = %q, want 1.0.1", got)
	}
	if deps := readNodeDeps(t, dir, "demo-app"); deps["demo-client"] != "1.1.0" {
		t.Errorf("demo-app range(demo-client) = %q, want 1.1.0", deps["demo-client"])
	}

	// Changelogs: one per released package, vs the self-authored goldens.
	for _, rel := range []string{
		"src/Demo.Core", "packages/demo-client", "packages/demo-app", "go-lib", "rust/demo-rs",
	} {
		want := normalize(readFile(t, filepath.Join(corpusDir, "polyglot", filepath.FromSlash(rel), "CHANGELOG.md")))
		got := normalize(readFile(t, filepath.Join(dir, filepath.FromSlash(rel), "CHANGELOG.md")))
		if got != want {
			t.Errorf("changelog(%s) diverges from the polyglot golden:\n%s", rel, diff(want, got))
		}
	}

	// The changeset is consumed like any version run.
	if fileExists(filepath.Join(dir, ".changeset", "c.md")) {
		t.Error("changeset should be consumed")
	}
}

func matchOne(t *testing.T, text, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("pattern %q not found in:\n%s", pattern, firstLines(text, 5))
	}
	return m[1]
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return fmt.Sprint(strings.Join(lines, "\n"))
}
