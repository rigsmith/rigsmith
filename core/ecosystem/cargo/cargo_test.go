package cargo

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

func TestDiscoverWorkspace(t *testing.T) {
	root := t.TempDir()

	// Virtual workspace manifest at the root: members only, no [package]. It must
	// be skipped for discovery, but its member crates are found by the walk.
	writeFile(t, filepath.Join(root, "Cargo.toml"), `[workspace]
members = ["crates/app", "crates/core"]
`)
	writeFile(t, filepath.Join(root, "crates", "core", "Cargo.toml"), `[package]
name = "core"
version = "0.1.0"
`)
	writeFile(t, filepath.Join(root, "crates", "app", "Cargo.toml"), `[package]
name = "app"
version = "0.2.0"

[dependencies]
core = { path = "../core", version = "0.1" }
serde = "1.0"
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
		t.Fatalf("expected 2 crates (app, core), got %d: %+v", len(byName), resp.Packages)
	}

	core := byName["core"]
	if core.Version != "0.1.0" {
		t.Errorf("core version = %q, want 0.1.0", core.Version)
	}
	app := byName["app"]
	if app.Version != "0.2.0" {
		t.Errorf("app version = %q, want 0.2.0", app.Version)
	}

	// Only the intra-repo `core` edge survives; serde is dropped.
	if len(app.Dependencies) != 1 {
		t.Fatalf("app deps = %+v, want one edge to core", app.Dependencies)
	}
	dep := app.Dependencies[0]
	if dep.Name != "core" || dep.Kind != plugin.DepNormal || dep.Range != "0.1" {
		t.Errorf("app->core dep = %+v, want {core normal 0.1}", dep)
	}
}

func TestSetVersionAndDeps(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Cargo.toml")
	writeFile(t, manifest, `[package]
name = "app"
version = "0.2.0"

[dependencies]
core = { path = "../core", version = "0.1" }
`)

	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:          root,
		Package:           plugin.Package{ManifestPath: "Cargo.toml"},
		NewVersion:        "0.3.0",
		DependencyUpdates: []plugin.DependencyUpdate{{Name: "core", NewVersion: "0.2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if !strings.Contains(string(got), `version = "0.3.0"`) {
		t.Errorf("package version not updated: %s", got)
	}
	if !strings.Contains(string(got), `version = "0.2"`) {
		t.Errorf("dep range not updated: %s", got)
	}
	// The package name line must be untouched (table-scoped rewrite).
	if !strings.Contains(string(got), `name = "app"`) {
		t.Errorf("name line clobbered: %s", got)
	}
}

// TestPublishPrivateSkipped checks that a publish=false crate is skipped without
// ever shelling out to cargo (hermetic — no toolchain required).
func TestPublishPrivateSkipped(t *testing.T) {
	a := New()
	resp, err := a.Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: t.TempDir(),
		Package:  plugin.Package{Name: "app", Version: "0.1.0", Dir: ".", Private: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Published || !resp.Skipped || resp.Message != "private" {
		t.Errorf("private publish = %+v, want {Skipped private}", resp)
	}
}
