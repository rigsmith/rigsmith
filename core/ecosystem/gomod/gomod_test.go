package gomod

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

	// Module a: annotated version.
	writeFile(t, filepath.Join(root, "a", "go.mod"), `module github.com/acme/a // rigsmith:version 1.4.0

go 1.26
`)
	// Module b: no annotation (defaults to 0.0.0), requires a and an external dep.
	writeFile(t, filepath.Join(root, "b", "go.mod"), `module github.com/acme/b

go 1.26

require (
	github.com/acme/a v1.4.0
	github.com/external/thing v0.2.0
)
`)
	// vendor must be skipped.
	writeFile(t, filepath.Join(root, "vendor", "x", "go.mod"), `module github.com/vendored/x
`)

	a := New()
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]plugin.Package{}
	for _, p := range resp.Packages {
		byName[p.Name] = p
	}
	if len(byName) != 2 {
		t.Fatalf("expected 2 modules, got %d: %v", len(byName), byName)
	}

	ma := byName["github.com/acme/a"]
	if ma.Version != "1.4.0" {
		t.Errorf("a version = %q, want 1.4.0", ma.Version)
	}

	mb := byName["github.com/acme/b"]
	if mb.Version != "0.0.0" {
		t.Errorf("b version = %q, want 0.0.0 (default)", mb.Version)
	}
	if len(mb.Dependencies) != 1 {
		t.Fatalf("b deps = %+v, want only the intra-repo edge to a", mb.Dependencies)
	}
	if mb.Dependencies[0].Name != "github.com/acme/a" || mb.Dependencies[0].Range != "v1.4.0" {
		t.Errorf("b dep = %+v, want {github.com/acme/a v1.4.0}", mb.Dependencies[0])
	}
}

// TestParseModuleForeignComment pins the fix that a FOREIGN trailing comment on
// the module line (e.g. a `// Deprecated:` directive) no longer makes parseModule
// return "" and silently skip the module. The canonical cases (rigsmith
// annotation, and a bare module line) are asserted alongside to document intent.
func TestParseModuleForeignComment(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantModule  string
		wantVersion string
	}{
		{
			name:        "foreign comment tolerated",
			line:        "module example.com/foo // Deprecated: use bar",
			wantModule:  "example.com/foo",
			wantVersion: defaultVersion, // no rigsmith annotation → default
		},
		{
			name:        "rigsmith annotation still captured",
			line:        "module example.com/foo // rigsmith:version 1.2.3",
			wantModule:  "example.com/foo",
			wantVersion: "1.2.3",
		},
		{
			name:        "bare module line",
			line:        "module example.com/foo",
			wantModule:  "example.com/foo",
			wantVersion: defaultVersion,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text := c.line + "\n\ngo 1.26\n"
			mod, ver := parseModule(text)
			if mod != c.wantModule {
				t.Errorf("module = %q, want %q", mod, c.wantModule)
			}
			if ver != c.wantVersion {
				t.Errorf("version = %q, want %q", ver, c.wantVersion)
			}
		})
	}
}

// TestSetVersionCommentForeignComment pins that setVersionComment does NOT
// clobber a pre-existing foreign comment on the module line (go.mod allows one
// comment per line; the authoritative version is the git tag). A clean line gets
// the annotation appended; a line with an existing rigsmith annotation has it
// replaced.
func TestSetVersionCommentForeignComment(t *testing.T) {
	// Foreign comment is preserved untouched.
	foreign := "module example.com/foo // Deprecated: use bar\n\ngo 1.26\n"
	got := setVersionComment(foreign, "2.0.0")
	if !strings.Contains(got, "// Deprecated: use bar") {
		t.Errorf("foreign comment clobbered: %q", got)
	}
	if strings.Contains(got, "rigsmith:version") {
		t.Errorf("annotation should not be buried behind a foreign comment: %q", got)
	}

	// Clean line: annotation appended.
	clean := "module example.com/foo\n\ngo 1.26\n"
	got = setVersionComment(clean, "2.0.0")
	if !strings.Contains(got, "module example.com/foo // rigsmith:version 2.0.0") {
		t.Errorf("annotation not appended to clean line: %q", got)
	}

	// Existing rigsmith annotation: replaced, not duplicated.
	annotated := "module example.com/foo // rigsmith:version 1.0.0\n\ngo 1.26\n"
	got = setVersionComment(annotated, "2.0.0")
	if strings.Count(got, "rigsmith:version") != 1 || !strings.Contains(got, "rigsmith:version 2.0.0") {
		t.Errorf("annotation not replaced in place: %q", got)
	}
}

func TestSetVersionCreatesComment(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "go.mod")
	writeFile(t, manifest, "module github.com/acme/a\n\ngo 1.26\n")

	a := New()
	if err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "go.mod"},
		NewVersion: "2.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if !strings.Contains(string(got), "module github.com/acme/a // rigsmith:version 2.0.0") {
		t.Errorf("annotation not created: %q", got)
	}

	// Updating again should replace, not duplicate.
	if err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "go.mod"},
		NewVersion: "2.1.0",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(manifest)
	if strings.Count(string(got), "rigsmith:version") != 1 {
		t.Errorf("expected exactly one annotation, got: %q", got)
	}
	if !strings.Contains(string(got), "rigsmith:version 2.1.0") {
		t.Errorf("annotation not updated: %q", got)
	}
}
