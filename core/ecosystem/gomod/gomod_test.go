package gomod

import (
	"context"
	"os"
	"path/filepath"
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

// TestArtifactsNoGoreleaserSkipped: a Go module with no .goreleaser.yaml has no
// binaries to ship, so artifacts skips (hermetic — no toolchain required).
func TestArtifactsNoGoreleaserSkipped(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		Package:   plugin.Package{ManifestPath: "go.mod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || !resp.Skipped || !strings.Contains(resp.Message, "no .goreleaser.yaml") {
		t.Errorf("no-goreleaser artifacts = %+v, want a skipped/no-config result", resp)
	}
}

// TestArtifactsDryRun: with a .goreleaser.yaml present, dry-run reports the
// goreleaser invocation without running it; snapshot switches the flag.
func TestArtifactsDryRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".goreleaser.yaml"), "version: 2\n")
	a := New()

	resp, err := a.Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot: root, OutputDir: t.TempDir(), DryRun: true,
		Package: plugin.Package{ManifestPath: "go.mod", Dir: ".", Version: "1.2.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Order-independent: the bumped version is injected as GORELEASER_CURRENT_TAG
	// (no git tag needed), and tag validation is skipped.
	// The dry-run message mirrors the real command exactly: --clean,
	// --skip=publish,validate, and the injected version tag.
	if resp.Built || resp.Skipped ||
		!strings.Contains(resp.Message, "--clean") ||
		!strings.Contains(resp.Message, "--skip=publish,validate") ||
		!strings.Contains(resp.Message, "GORELEASER_CURRENT_TAG=v1.2.3") {
		t.Errorf("dry-run artifacts = %+v, want a tag-injecting goreleaser message", resp)
	}

	snap, err := a.Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot: root, OutputDir: t.TempDir(), DryRun: true, Snapshot: true,
		Package: plugin.Package{ManifestPath: "go.mod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Message, "--snapshot") {
		t.Errorf("snapshot dry-run = %+v, want a --snapshot message", snap)
	}
}

// TestCollectDist keeps only the shippable archives + checksums from a goreleaser
// dist/ directory, dropping raw binaries and metadata.
func TestCollectDist(t *testing.T) {
	dist := t.TempDir()
	for _, n := range []string{
		"rig_1.0.0_darwin_arm64.tar.gz",
		"rig_1.0.0_windows_amd64.zip",
		"checksums.txt",
		"metadata.json", // dropped
		"rig",           // raw binary, dropped
	} {
		writeFile(t, filepath.Join(dist, n), "x")
	}
	arts, err := collectDist(dist)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{} // base name -> kind
	for _, art := range arts {
		if !art.Attach {
			t.Errorf("%s should be Attach:true", art.Path)
		}
		got[filepath.Base(art.Path)] = art.Kind
	}
	want := map[string]string{
		"rig_1.0.0_darwin_arm64.tar.gz": plugin.ArtifactArchive,
		"rig_1.0.0_windows_amd64.zip":   plugin.ArtifactArchive,
		"checksums.txt":                 plugin.ArtifactChecksum,
	}
	if len(got) != len(want) {
		t.Fatalf("collectDist = %v, want %v", got, want)
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("%s kind = %q, want %q", name, got[name], kind)
		}
	}
}

// TestInfoAdvertisesArtifacts locks the Go adapter's artifacts capability.
func TestInfoAdvertisesArtifacts(t *testing.T) {
	found := false
	for _, c := range New().Info().Capabilities {
		if c == plugin.MethodArtifacts {
			found = true
		}
	}
	if !found {
		t.Error("go Info() should advertise MethodArtifacts")
	}
}

func TestInfoAdvertisesReleaseInit(t *testing.T) {
	found := false
	for _, c := range New().Info().Capabilities {
		if c == plugin.MethodReleaseInit {
			found = true
		}
	}
	if !found {
		t.Error("go Info() should advertise MethodReleaseInit")
	}
}

// TestReleaseInitWithBinaries: a module with main packages under cmd/ gets a
// goreleaser starter templated from those mains (sorted), the GITHUB_TOKEN
// preflight, and a note listing the binaries.
func TestReleaseInitWithBinaries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/acme/tool\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "cmd", "rig", "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "foo", "main.go"), "package main\n\nfunc main() {}\n")
	// A library package and a test file must not be mistaken for binaries.
	writeFile(t, filepath.Join(root, "internal", "lib", "lib.go"), "package lib\n")
	writeFile(t, filepath.Join(root, "cmd", "rig", "main_test.go"), "package main\n")

	resp, err := New().ReleaseInit(context.Background(), plugin.ReleaseInitRequest{
		RepoRoot: root,
		Packages: []plugin.Package{{Name: "tool", Dir: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BuildConfig == nil {
		t.Fatal("expected a BuildConfig for a module with binaries")
	}
	bc := resp.BuildConfig
	if bc.Present {
		t.Error("BuildConfig.Present should be false when no .goreleaser.yaml exists")
	}
	if bc.Path != ".goreleaser.yaml" || bc.Tool != "goreleaser" {
		t.Errorf("unexpected BuildConfig path/tool: %q / %q", bc.Path, bc.Tool)
	}
	for _, want := range []string{"project_name: " + filepath.Base(root), "main: ./cmd/foo", "main: ./cmd/rig", "binary: rig", "binary: foo"} {
		if !strings.Contains(bc.Content, want) {
			t.Errorf("starter goreleaser missing %q\n%s", want, bc.Content)
		}
	}
	// foo sorts before rig — the build blocks should appear in that order.
	if strings.Index(bc.Content, "id: foo") > strings.Index(bc.Content, "id: rig") {
		t.Error("build blocks should be sorted by main path (foo before rig)")
	}
	if !hasToken(resp.Tokens, "GITHUB_TOKEN") {
		t.Error("expected a GITHUB_TOKEN preflight")
	}
	if len(resp.Notes) == 0 || !strings.Contains(resp.Notes[0], "rig") {
		t.Errorf("expected a note naming the binaries, got %v", resp.Notes)
	}
}

// TestReleaseInitPresentGoreleaser: an existing config is reported, not regenerated.
func TestReleaseInitPresentGoreleaser(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/acme/tool\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "cmd", "rig", "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, ".goreleaser.yaml"), "version: 2\n")

	resp, err := New().ReleaseInit(context.Background(), plugin.ReleaseInitRequest{
		RepoRoot: root,
		Packages: []plugin.Package{{Name: "tool", Dir: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BuildConfig == nil || !resp.BuildConfig.Present {
		t.Fatal("expected BuildConfig.Present=true when .goreleaser.yaml exists")
	}
	if resp.BuildConfig.Content != "" {
		t.Error("should not template content over an existing config")
	}
}

// TestReleaseInitNoBinaries: a library-only module ships by tag — no build config,
// but still the GITHUB_TOKEN preflight.
func TestReleaseInitNoBinaries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/acme/lib\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "lib.go"), "package lib\n")

	resp, err := New().ReleaseInit(context.Background(), plugin.ReleaseInitRequest{
		RepoRoot: root,
		Packages: []plugin.Package{{Name: "lib", Dir: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BuildConfig != nil {
		t.Error("a library-only module should have no BuildConfig")
	}
	if !hasToken(resp.Tokens, "GITHUB_TOKEN") {
		t.Error("expected a GITHUB_TOKEN preflight even for a library module")
	}
}

func hasToken(tokens []plugin.TokenSpec, envVar string) bool {
	for _, t := range tokens {
		if t.EnvVar == envVar {
			return true
		}
	}
	return false
}
