package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// stubEco is a minimal in-test plugin.Ecosystem: it always detects and returns a
// fixed package list, so discovery reconciliation can be exercised without real
// manifests on disk. Overlays declares the base ids this stub sits on top of.
type stubEco struct {
	id       string
	overlays []string
	packages []plugin.Package
}

func (s stubEco) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{APIVersion: plugin.APIVersion, ID: s.id, Overlays: s.overlays}
}
func (s stubEco) Detect(context.Context, string) (bool, error) { return true, nil }
func (s stubEco) Discover(context.Context, plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	return plugin.DiscoverResponse{Packages: s.packages}, nil
}
func (s stubEco) SetVersion(context.Context, plugin.SetVersionRequest) error { return nil }
func (s stubEco) Publish(context.Context, plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{}, nil
}
func (s stubEco) Artifacts(context.Context, plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	return plugin.ArtifactsResponse{}, nil
}
func (s stubEco) ReleaseInit(context.Context, plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	return plugin.ReleaseInitResponse{}, nil
}

// registryOf builds a Registry from the given stub ecosystems, in order.
func registryOf(ecos ...plugin.Ecosystem) *plugin.Registry {
	r := plugin.NewRegistry()
	for _, e := range ecos {
		r.Register(e)
	}
	return r
}

// TestDiscoverOverlayClaimsBaseDir: an overlay ecosystem sharing a base package's
// directory takes over that unit — the base package is dropped, the overlay's
// survives, and an unrelated base package in another directory is untouched.
func TestDiscoverOverlayClaimsBaseDir(t *testing.T) {
	base := stubEco{id: "base", packages: []plugin.Package{
		{Name: "app", Version: "1.0.0", Dir: "src", ManifestPath: "src/Cargo.toml",
			Dependencies: []plugin.Dependency{{Name: "lib", Kind: plugin.DepNormal, Range: "2.0.0"}}},
		{Name: "lib", Version: "2.0.0", Dir: "lib", ManifestPath: "lib/Cargo.toml"},
	}}
	overlay := stubEco{id: "deck", overlays: []string{"base"}, packages: []plugin.Package{
		{Name: "app-desktop", Version: "1.0.0", Dir: "src", ManifestPath: "src/app.conf.json"},
	}}
	ws := &Workspace{Root: t.TempDir(), Config: config.Default(), Registry: registryOf(base, overlay)}

	pkgs, ecoOf, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	byName := map[string]plugin.Package{}
	for _, p := range pkgs {
		got[p.Name] = ecoOf[p.Name]
		byName[p.Name] = p
	}
	if _, dropped := got["app"]; dropped {
		t.Errorf("base package \"app\" in src/ should be claimed by the overlay; got %v", got)
	}
	if got["app-desktop"] != "deck" {
		t.Errorf("overlay package should survive as \"deck\"; got %v", got)
	}
	if got["lib"] != "base" {
		t.Errorf("unrelated base package \"lib\" should be untouched; got %v", got)
	}
	// The overlay (no deps of its own) inherits the dropped base's edge to lib, so
	// the cascade still bumps the desktop app when lib changes.
	deps := byName["app-desktop"].Dependencies
	if len(deps) != 1 || deps[0].Name != "lib" {
		t.Errorf("overlay should inherit base's dependency on lib; got %+v", deps)
	}
}

// TestDiscoverOverlayDifferentDirNoClaim: an overlay only claims its own
// directory — a base package elsewhere is not dropped even though the overlay
// declares it as a base.
func TestDiscoverOverlayDifferentDirNoClaim(t *testing.T) {
	base := stubEco{id: "base", packages: []plugin.Package{
		{Name: "app", Version: "1.0.0", Dir: "src", ManifestPath: "src/Cargo.toml"},
	}}
	overlay := stubEco{id: "deck", overlays: []string{"base"}, packages: []plugin.Package{
		{Name: "other", Version: "1.0.0", Dir: "elsewhere", ManifestPath: "elsewhere/app.conf.json"},
	}}
	ws := &Workspace{Root: t.TempDir(), Config: config.Default(), Registry: registryOf(base, overlay)}

	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range pkgs {
		names[p.Name] = true
	}
	if !names["app"] || !names["other"] {
		t.Errorf("both packages should survive (different dirs); got %v", names)
	}
}

func writeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "module " + module + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverPerEcosystemSourcePath: a per-ecosystem `sourcePath` narrows that
// ecosystem's discovery to a subtree, overriding the whole-repo default.
func TestDiscoverPerEcosystemSourcePath(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, filepath.Join(root, "keep", "mod-a"), "example.com/a")
	writeGoMod(t, filepath.Join(root, "skip", "mod-b"), "example.com/b")

	cfg, err := config.Parse([]byte(`{ "go": { "sourcePath": "keep" } }`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: root, Config: cfg, Registry: ecosystem.Default()}

	pkgs, ecoOf, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range pkgs {
		got[p.Name] = true
	}
	if !got["example.com/a"] {
		t.Errorf("expected example.com/a (under keep/) to be discovered; got %v", got)
	}
	if got["example.com/b"] {
		t.Errorf("example.com/b (under skip/) should be excluded by sourcePath; got %v", got)
	}
	if ecoOf["example.com/a"] != "go" {
		t.Errorf("ecoOf = %v", ecoOf)
	}
}

// TestDiscoverTauriOverlaysCargo is the end-to-end check with the real registry:
// a src-tauri app crate is owned by the tauri adapter (cargo's package for the
// same dir is dropped), while a sibling library crate stays cargo-owned.
func TestDiscoverTauriOverlaysCargo(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "src-tauri", "Cargo.toml"), "[package]\nname = \"myapp\"\nversion = \"0.1.0\"\npublish = false\n")
	writeFileAt(t, filepath.Join(root, "src-tauri", "tauri.conf.json"), `{ "version": "0.1.0", "productName": "My App" }`)
	writeFileAt(t, filepath.Join(root, "crates", "lib", "Cargo.toml"), "[package]\nname = \"lib\"\nversion = \"0.4.0\"\n")

	ws := &Workspace{Root: root, Config: config.Default(), Registry: ecosystem.Default()}
	pkgs, ecoOf, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	owners := map[string]string{}
	for _, p := range pkgs {
		owners[p.Name] = ecoOf[p.Name]
	}
	if owners["myapp"] != "tauri" {
		t.Errorf("myapp owner = %q, want tauri (overlay claims the app crate)", owners["myapp"])
	}
	if owners["lib"] != "cargo" {
		t.Errorf("lib owner = %q, want cargo (untouched library crate)", owners["lib"])
	}
	// Exactly one package per unit — the cargo duplicate of myapp must be gone.
	count := 0
	for _, p := range pkgs {
		if p.Name == "myapp" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one myapp package, got %d", count)
	}
}

// TestDiscoverElectronOverlaysNode: in a node workspace with an Electron app that
// depends on a sibling library, the app is owned by the electron adapter (node's
// duplicate dropped) and inherits node's intra-repo edge to the library, while the
// library stays node-owned.
func TestDiscoverElectronOverlaysNode(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "package.json"), `{"name":"root","private":true,"workspaces":["packages/*"]}`)
	writeFileAt(t, filepath.Join(root, "packages", "desktop", "package.json"),
		`{"name":"desktop","version":"1.0.0","private":true,"devDependencies":{"electron":"^30"},"dependencies":{"lib":"^0.9.0"}}`)
	writeFileAt(t, filepath.Join(root, "packages", "lib", "package.json"), `{"name":"lib","version":"0.9.0"}`)

	ws := &Workspace{Root: root, Config: config.Default(), Registry: ecosystem.Default()}
	pkgs, ecoOf, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]plugin.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	if ecoOf["desktop"] != "electron" {
		t.Errorf("desktop owner = %q, want electron", ecoOf["desktop"])
	}
	if ecoOf["lib"] != "node" {
		t.Errorf("lib owner = %q, want node", ecoOf["lib"])
	}
	deps := byName["desktop"].Dependencies
	if len(deps) != 1 || deps[0].Name != "lib" {
		t.Errorf("electron app should inherit node's edge to lib; got %+v", deps)
	}
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverWholeRepoByDefault: without a sourcePath, both subtrees are found.
func TestDiscoverWholeRepoByDefault(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, filepath.Join(root, "keep", "mod-a"), "example.com/a")
	writeGoMod(t, filepath.Join(root, "skip", "mod-b"), "example.com/b")

	ws := &Workspace{Root: root, Config: config.Default(), Registry: ecosystem.Default()}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Errorf("expected both modules without a sourcePath, got %d: %+v", len(pkgs), pkgs)
	}
}
