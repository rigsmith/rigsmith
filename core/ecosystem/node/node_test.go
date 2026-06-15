package node

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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

func TestDiscoverPnpmWorkspace(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "package.json"), `{
  "name": "root",
  "version": "0.0.0",
  "private": true
}`)
	writeFile(t, filepath.Join(root, "pnpm-workspace.yaml"), `packages:
  - 'packages/*'
`)
	writeFile(t, filepath.Join(root, "packages", "a", "package.json"), `{
  "name": "@acme/a",
  "version": "1.0.0"
}`)
	writeFile(t, filepath.Join(root, "packages", "b", "package.json"), `{
  "name": "@acme/b",
  "version": "2.0.0",
  "dependencies": {
    "@acme/a": "^1.0.0",
    "left-pad": "^1.3.0"
  },
  "devDependencies": {
    "@acme/a": "^1.0.0"
  }
}`)

	a := New()
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]plugin.Package{}
	for _, p := range resp.Packages {
		byName[p.Name] = p
	}
	// The root manifest is the workspace container, not a workspace package.
	if _, ok := byName["root"]; ok {
		t.Errorf("root package must not be discovered, got %v", names(byName))
	}
	a0 := byName["@acme/a"]
	if a0.Version != "1.0.0" {
		t.Errorf("@acme/a version = %q, want 1.0.0", a0.Version)
	}
	b := byName["@acme/b"]
	if b.Version != "2.0.0" {
		t.Errorf("@acme/b version = %q, want 2.0.0", b.Version)
	}
	// Only the intra-repo @acme/a edges survive; left-pad is dropped. Expect one
	// normal and one dev edge to @acme/a.
	var normal, dev int
	for _, d := range b.Dependencies {
		if d.Name != "@acme/a" {
			t.Errorf("unexpected dep %q (only intra-repo expected)", d.Name)
		}
		if d.Range != "^1.0.0" {
			t.Errorf("dep range = %q, want ^1.0.0", d.Range)
		}
		switch d.Kind {
		case plugin.DepNormal:
			normal++
		case plugin.DepDev:
			dev++
		}
	}
	if normal != 1 || dev != 1 {
		t.Errorf("@acme/b deps = %+v, want one normal + one dev edge to @acme/a", b.Dependencies)
	}
}

// discoverNames runs Discover and returns the discovered package names, sorted.
func discoverNames(t *testing.T, root string) []string {
	t.Helper()
	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range resp.Packages {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

func wantNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("discovered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discovered %v, want %v", got, want)
		}
	}
}

// TestDiscoverNpmWorkspaceGlobs: the npm array form resolves the globs only —
// the root package itself, a directory outside the globs, and a dependency's
// own package.json are all excluded.
func TestDiscoverNpmWorkspaceGlobs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(root, "packages", "web", "package.json"), `{ "name": "@demo/web", "version": "1.0.0" }`)
	writeFile(t, filepath.Join(root, "packages", "api", "package.json"), `{ "name": "@demo/api", "version": "1.0.0" }`)
	// Not matched by the glob, so not a workspace package.
	writeFile(t, filepath.Join(root, "tools", "package.json"), `{ "name": "@demo/tooling", "version": "1.0.0" }`)
	// A dependency's own package.json must never be discovered.
	writeFile(t, filepath.Join(root, "packages", "web", "node_modules", "left-pad", "package.json"), `{ "name": "left-pad", "version": "1.0.0" }`)

	wantNames(t, discoverNames(t, root), "@demo/web", "@demo/api")
}

// TestDiscoverYarnObjectWorkspaces: the yarn { "packages": [...] } object form
// works, and a "**" glob matches nested directories at any depth.
func TestDiscoverYarnObjectWorkspaces(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{ "name": "root", "private": true, "workspaces": { "packages": ["packages/**"] } }`)
	writeFile(t, filepath.Join(root, "packages", "web", "package.json"), `{ "name": "@demo/web", "version": "1.0.0" }`)
	writeFile(t, filepath.Join(root, "packages", "group", "nested", "package.json"), `{ "name": "@demo/nested", "version": "1.0.0" }`)

	wantNames(t, discoverNames(t, root), "@demo/web", "@demo/nested")
}

// TestDiscoverNegatedWorkspacePatterns: a "!" pattern removes its matches from
// the workspace set.
func TestDiscoverNegatedWorkspacePatterns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*", "!packages/internal"] }`)
	writeFile(t, filepath.Join(root, "packages", "web", "package.json"), `{ "name": "@demo/web", "version": "1.0.0" }`)
	writeFile(t, filepath.Join(root, "packages", "internal", "package.json"), `{ "name": "@demo/internal", "version": "1.0.0" }`)

	wantNames(t, discoverNames(t, root), "@demo/web")
}

// TestDiscoverSkipsWorkspacePackagesWithoutName: a matched directory whose
// package.json has no "name" is skipped without an error.
func TestDiscoverSkipsWorkspacePackagesWithoutName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(root, "packages", "web", "package.json"), `{ "name": "@demo/web", "version": "1.0.0" }`)
	writeFile(t, filepath.Join(root, "packages", "noname", "package.json"), `{ "private": true }`)

	wantNames(t, discoverNames(t, root), "@demo/web")
}

// TestDiscoverMissingDirectoryReturnsEmpty: a nonexistent root and workspace
// globs that match nothing both yield an empty result, not an error.
func TestDiscoverMissingDirectoryReturnsEmpty(t *testing.T) {
	if got := discoverNames(t, filepath.Join(t.TempDir(), "does-not-exist")); len(got) != 0 {
		t.Fatalf("missing root: discovered %v, want none", got)
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*", "absent/dir"] }`)
	if got := discoverNames(t, root); len(got) != 0 {
		t.Fatalf("empty globs: discovered %v, want none", got)
	}
}

func TestDiscoverFallbackWalk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"solo","version":"3.2.1"}`)
	// node_modules must be skipped.
	writeFile(t, filepath.Join(root, "node_modules", "dep", "package.json"), `{"name":"dep","version":"9.9.9"}`)

	a := New()
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 || resp.Packages[0].Name != "solo" {
		t.Fatalf("expected only [solo], got %+v", resp.Packages)
	}
}

func TestSetVersionAndDeps(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "package.json")
	writeFile(t, manifest, `{
  "name": "@acme/b",
  "version": "2.0.0",
  "dependencies": {
    "@acme/a": "^1.0.0"
  }
}`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:          root,
		Package:           plugin.Package{ManifestPath: "package.json"},
		NewVersion:        "2.1.0",
		DependencyUpdates: []plugin.DependencyUpdate{{Name: "@acme/a", NewVersion: "^1.1.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if !strings.Contains(string(got), `"version": "2.1.0"`) {
		t.Errorf("version not updated: %s", got)
	}
	if !strings.Contains(string(got), `"@acme/a": "^1.1.0"`) {
		t.Errorf("dep range not updated: %s", got)
	}
}

// TestSetVersionOnlyTopLevelVersion pins the fix where SetVersion rewrote EVERY
// "version" key, including a nested publishConfig.version. Only the first/top-
// level "version" must change; the nested one is left intact.
func TestSetVersionOnlyTopLevelVersion(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "package.json")
	writeFile(t, manifest, `{
  "name": "@acme/lib",
  "version": "1.0.0",
  "publishConfig": {
    "version": "9.9.9"
  }
}`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "package.json"},
		NewVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if !strings.Contains(string(got), `"version": "2.0.0"`) {
		t.Errorf("top-level version not updated: %s", got)
	}
	if !strings.Contains(string(got), `"version": "9.9.9"`) {
		t.Errorf("nested publishConfig.version must stay 9.9.9: %s", got)
	}
	if strings.Contains(string(got), `"version": "2.0.0"`) && strings.Count(string(got), `"2.0.0"`) != 1 {
		t.Errorf("expected exactly one 2.0.0 occurrence: %s", got)
	}
}

func names(m map[string]plugin.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPublishPrivateSkipped checks that a private package is skipped before any
// `npm` invocation (hermetic — no toolchain required).
func TestPublishPrivateSkipped(t *testing.T) {
	a := New()
	resp, err := a.Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: t.TempDir(),
		Package:  plugin.Package{Name: "@acme/lib", Version: "1.0.0", Dir: ".", Private: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Published || !resp.Skipped || resp.Message != "private" {
		t.Errorf("private publish = %+v, want {Skipped private}", resp)
	}
}

// TestArtifactsPrivateSkipped checks a private package is skipped before any
// `npm pack` invocation (hermetic — no toolchain required).
func TestArtifactsPrivateSkipped(t *testing.T) {
	a := New()
	resp, err := a.Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		Package:   plugin.Package{Name: "@acme/lib", Version: "1.0.0", Dir: ".", Private: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || !resp.Skipped || resp.Message != "private" {
		t.Errorf("private artifacts = %+v, want {Skipped private}", resp)
	}
}

// TestArtifactsDryRun reports intent without running npm (hermetic).
func TestArtifactsDryRun(t *testing.T) {
	a := New()
	resp, err := a.Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		DryRun:    true,
		Package:   plugin.Package{Name: "@acme/lib", Version: "1.0.0", Dir: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || resp.Skipped || resp.Message != "dry-run: would npm pack @acme/lib@1.0.0" {
		t.Errorf("dry-run artifacts = %+v, want a would-npm-pack message", resp)
	}
}

// TestInfoAdvertisesArtifacts locks that the Node adapter declares the artifacts
// capability.
func TestInfoAdvertisesArtifacts(t *testing.T) {
	if !hasCapability(New().Info(), plugin.MethodArtifacts) {
		t.Error("node Info() should advertise MethodArtifacts")
	}
}

func hasCapability(info plugin.EcosystemInfo, want string) bool {
	for _, c := range info.Capabilities {
		if c == want {
			return true
		}
	}
	return false
}

// TestReleaseInit: Node packs natively (npm pack), so it declares only the
// NPM_TOKEN preflight and no build-config file.
func TestReleaseInit(t *testing.T) {
	if !hasCapability(New().Info(), plugin.MethodReleaseInit) {
		t.Fatal("node Info() should advertise MethodReleaseInit")
	}
	resp, err := New().ReleaseInit(context.Background(), plugin.ReleaseInitRequest{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BuildConfig != nil {
		t.Error("node has a native pack — no build-config file to scaffold")
	}
	if len(resp.Tokens) != 1 || resp.Tokens[0].EnvVar != "NPM_TOKEN" {
		t.Errorf("expected a single NPM_TOKEN preflight, got %+v", resp.Tokens)
	}
}
