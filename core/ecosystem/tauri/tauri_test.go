package tauri

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

// app lays down a src-tauri crate (Cargo.toml + tauri.conf.json) under root and
// returns the crate dir.
func app(t *testing.T, root, crateName, cargoVersion, confJSON string) string {
	t.Helper()
	dir := filepath.Join(root, "src-tauri")
	writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \""+crateName+"\"\nversion = \""+cargoVersion+"\"\npublish = false\n")
	writeFile(t, filepath.Join(dir, "tauri.conf.json"), confJSON)
	return dir
}

// TestDiscoverCargoSourced: with no "version" in tauri.conf.json, the sibling
// Cargo.toml is the source — version comes from it and VersionFile points at it.
func TestDiscoverCargoSourced(t *testing.T) {
	root := t.TempDir()
	app(t, root, "myapp", "0.2.0", `{ "productName": "My App" }`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 {
		t.Fatalf("expected 1 tauri app, got %d: %+v", len(resp.Packages), resp.Packages)
	}
	p := resp.Packages[0]
	if p.Name != "myapp" {
		t.Errorf("name = %q, want crate name myapp", p.Name)
	}
	if p.Version != "0.2.0" {
		t.Errorf("version = %q, want 0.2.0 from Cargo.toml", p.Version)
	}
	if p.Dir != "src-tauri" {
		t.Errorf("dir = %q, want src-tauri (must match the cargo crate dir)", p.Dir)
	}
	if p.VersionFile != filepath.Join("src-tauri", "Cargo.toml") {
		t.Errorf("versionFile = %q, want src-tauri/Cargo.toml", p.VersionFile)
	}
}

// TestDiscoverConfSourced: a semver "version" in tauri.conf.json wins; the conf
// becomes the version file.
func TestDiscoverConfSourced(t *testing.T) {
	root := t.TempDir()
	app(t, root, "myapp", "0.0.0", `{ "version": "1.2.3", "productName": "My App" }`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	p := resp.Packages[0]
	if p.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3 from tauri.conf.json", p.Version)
	}
	if p.VersionFile != filepath.Join("src-tauri", "tauri.conf.json") {
		t.Errorf("versionFile = %q, want src-tauri/tauri.conf.json", p.VersionFile)
	}
}

// TestDiscoverV1Nested: Tauri v1 nests version/productName under "package".
func TestDiscoverV1Nested(t *testing.T) {
	root := t.TempDir()
	app(t, root, "legacy", "0.0.0", `{ "package": { "version": "2.5.0", "productName": "Legacy" } }`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Packages[0].Version != "2.5.0" {
		t.Errorf("version = %q, want 2.5.0 from nested package.version", resp.Packages[0].Version)
	}
}

// TestDiscoverSkipsConfWithoutCrate: a tauri.conf.json with no sibling Cargo.toml
// is not a real Tauri app and is skipped.
func TestDiscoverSkipsConfWithoutCrate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "weird", "tauri.conf.json"), `{ "version": "1.0.0" }`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 0 {
		t.Errorf("expected no packages (no sibling Cargo.toml), got %+v", resp.Packages)
	}
}

// TestDiscoverJSON5: a tauri.conf.json5 with comments parses (conf-sourced).
func TestDiscoverJSON5(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src-tauri")
	writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"json5app\"\nversion = \"0.0.0\"\n")
	writeFile(t, filepath.Join(dir, "tauri.conf.json5"), "{\n  // app version\n  \"version\": \"3.1.0\",\n  \"productName\": \"J5\", // trailing comma ok\n}")

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 || resp.Packages[0].Version != "3.1.0" {
		t.Fatalf("json5 conf: want one pkg @3.1.0, got %+v", resp.Packages)
	}

	// SetVersion stamps the json5 conf (quoted keys) and the crate in lockstep.
	err = New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: filepath.Join("src-tauri", "tauri.conf.json5")},
		NewVersion: "3.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "tauri.conf.json5"))
	cargo, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(conf), `"version": "3.2.0"`) || !strings.Contains(string(cargo), `version = "3.2.0"`) {
		t.Errorf("json5 lockstep stamp failed:\nconf=%s\ncargo=%s", conf, cargo)
	}
}

// TestDiscoverTomlConf: a Tauri.toml (top-level version) parses and stamps.
func TestDiscoverTomlConf(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src-tauri")
	writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"tomlapp\"\nversion = \"0.0.0\"\n")
	writeFile(t, filepath.Join(dir, "Tauri.toml"), "version = \"1.5.0\"\nproductName = \"T\"\n")

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 || resp.Packages[0].Version != "1.5.0" {
		t.Fatalf("toml conf: want one pkg @1.5.0, got %+v", resp.Packages)
	}

	err = New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: filepath.Join("src-tauri", "Tauri.toml")},
		NewVersion: "1.6.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "Tauri.toml"))
	cargo, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(conf), `version = "1.6.0"`) || !strings.Contains(string(cargo), `version = "1.6.0"`) {
		t.Errorf("toml lockstep stamp failed:\nconf=%s\ncargo=%s", conf, cargo)
	}
}

// TestSetVersionConfSourcedLockstep: in conf-sourced mode both tauri.conf.json
// and Cargo.toml are stamped to the new version (Decision #3).
func TestSetVersionConfSourcedLockstep(t *testing.T) {
	root := t.TempDir()
	dir := app(t, root, "myapp", "1.2.3", `{
  "version": "1.2.3",
  "productName": "My App"
}`)

	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: filepath.Join("src-tauri", "tauri.conf.json")},
		NewVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "tauri.conf.json"))
	if !strings.Contains(string(conf), `"version": "2.0.0"`) {
		t.Errorf("tauri.conf.json version not updated: %s", conf)
	}
	cargo, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(cargo), `version = "2.0.0"`) {
		t.Errorf("Cargo.toml not kept in lockstep: %s", cargo)
	}
	if !strings.Contains(string(cargo), `name = "myapp"`) {
		t.Errorf("crate name clobbered: %s", cargo)
	}
}

// TestSetVersionCargoSourced: with no version in the conf, only Cargo.toml is
// stamped and the conf is left untouched (no version field is invented).
func TestSetVersionCargoSourced(t *testing.T) {
	root := t.TempDir()
	dir := app(t, root, "myapp", "0.2.0", `{ "productName": "My App" }`)

	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: filepath.Join("src-tauri", "tauri.conf.json")},
		NewVersion: "0.3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	cargo, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(cargo), `version = "0.3.0"`) {
		t.Errorf("Cargo.toml version not updated: %s", cargo)
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "tauri.conf.json"))
	if strings.Contains(string(conf), "version") {
		t.Errorf("conf should not gain a version field in cargo-sourced mode: %s", conf)
	}
}

// TestSetVersionDependencyRange: an intra-repo dep range in the crate is rewritten.
func TestSetVersionDependencyRange(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src-tauri")
	writeFile(t, filepath.Join(dir, "Cargo.toml"), `[package]
name = "myapp"
version = "0.2.0"

[dependencies]
core = { path = "../core", version = "0.1" }
`)
	writeFile(t, filepath.Join(dir, "tauri.conf.json"), `{ "productName": "My App" }`)

	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:          root,
		Package:           plugin.Package{ManifestPath: filepath.Join("src-tauri", "tauri.conf.json")},
		NewVersion:        "0.3.0",
		DependencyUpdates: []plugin.DependencyUpdate{{Name: "core", NewVersion: "0.2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cargo, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(cargo), `version = "0.2"`) {
		t.Errorf("dep range not updated: %s", cargo)
	}
}

// TestPublishNoOp: a Tauri app is never registry-published.
func TestPublishNoOp(t *testing.T) {
	resp, err := New().Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: t.TempDir(),
		Package:  plugin.Package{Name: "myapp", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Published || !resp.Skipped {
		t.Errorf("publish = %+v, want a skip", resp)
	}
}

// TestArtifactsDryRun reports intent without running the toolchain (hermetic).
func TestArtifactsDryRun(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot: t.TempDir(),
		DryRun:   true,
		Package:  plugin.Package{Name: "myapp", Version: "1.0.0", Dir: "src-tauri"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || resp.Skipped || !strings.Contains(resp.Message, "would cargo tauri build myapp@1.0.0") {
		t.Errorf("dry-run artifacts = %+v", resp)
	}
}

// TestMergeSigningEnv appends sorted KEY=VALUE entries, and leaves the base alone
// when there is nothing to sign with.
func TestMergeSigningEnv(t *testing.T) {
	base := []string{"PATH=/x"}
	if got := mergeSigningEnv(base, nil); len(got) != 1 || got[0] != "PATH=/x" {
		t.Errorf("nil signing should return base unchanged, got %v", got)
	}
	got := mergeSigningEnv(base, &plugin.SigningCreds{Env: map[string]string{"B": "2", "A": "1"}})
	want := []string{"PATH=/x", "A=1", "B=2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mergeSigningEnv = %v, want %v", got, want)
	}
}

// TestArtifactsDryRunSigned: a dry-run with signing credentials reports it.
func TestArtifactsDryRunSigned(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot: t.TempDir(), DryRun: true,
		Package: plugin.Package{Name: "myapp", Version: "1.0.0", Dir: "src-tauri"},
		Signing: &plugin.SigningCreds{Env: map[string]string{"APPLE_ID": "me@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Message, "(signed)") {
		t.Errorf("signed dry-run should note (signed); got %q", resp.Message)
	}
}

// TestInfoCapabilities locks the Tauri adapter's contract: Artifacts yes, Publish
// no (it overlays cargo, not a registry), and it overlays cargo.
func TestInfoCapabilities(t *testing.T) {
	info := New().Info()
	caps := map[string]bool{}
	for _, c := range info.Capabilities {
		caps[c] = true
	}
	if !caps[plugin.MethodArtifacts] {
		t.Error("Tauri should advertise MethodArtifacts")
	}
	if caps[plugin.MethodPublish] {
		t.Error("Tauri should NOT advertise MethodPublish (no registry push)")
	}
	if len(info.Overlays) != 1 || info.Overlays[0] != "cargo" {
		t.Errorf("Overlays = %v, want [cargo]", info.Overlays)
	}
}

// TestCollectBundles gathers installers, the updater manifest and its signature,
// while ignoring unrelated files.
func TestCollectBundles(t *testing.T) {
	bundleDir := t.TempDir()
	writeFile(t, filepath.Join(bundleDir, "dmg", "My App_1.0.0_aarch64.dmg"), "x")
	writeFile(t, filepath.Join(bundleDir, "deb", "my-app_1.0.0_amd64.deb"), "x")
	writeFile(t, filepath.Join(bundleDir, "appimage", "my-app_1.0.0.AppImage"), "x")
	writeFile(t, filepath.Join(bundleDir, "macos", "My App.app.tar.gz"), "x")
	writeFile(t, filepath.Join(bundleDir, "macos", "My App.app.tar.gz.sig"), "x")
	writeFile(t, filepath.Join(bundleDir, "latest.json"), "{}")
	writeFile(t, filepath.Join(bundleDir, "notes.txt"), "ignore me")

	arts := collectBundles(bundleDir)
	got := map[string]bool{}
	for _, a := range arts {
		if !a.Attach {
			t.Errorf("artifact %s should be Attach:true", a.Path)
		}
		got[filepath.Base(a.Path)] = true
	}
	for _, want := range []string{"My App_1.0.0_aarch64.dmg", "my-app_1.0.0_amd64.deb", "my-app_1.0.0.AppImage", "My App.app.tar.gz", "My App.app.tar.gz.sig", "latest.json"} {
		if !got[want] {
			t.Errorf("expected %q among collected bundles; got %v", want, got)
		}
	}
	if got["notes.txt"] {
		t.Error("notes.txt should not be collected")
	}
}

// TestInstallerKind maps extensions, preferring the longer .app.tar.gz.
func TestInstallerKind(t *testing.T) {
	cases := map[string]string{
		"app.app.tar.gz":     plugin.ArtifactArchive,
		"app_1.0_amd64.deb":  plugin.ArtifactBinary,
		"app.dmg":            plugin.ArtifactBinary,
		"app_x64-setup.exe":  plugin.ArtifactBinary,
		"app_1.0.0.appimage": plugin.ArtifactBinary,
	}
	for name, wantKind := range cases {
		kind, ok := installerKind(name)
		if !ok || kind != wantKind {
			t.Errorf("installerKind(%q) = %q,%v; want %q,true", name, kind, ok, wantKind)
		}
	}
	if _, ok := installerKind("readme.txt"); ok {
		t.Error("installerKind(readme.txt) should be false")
	}
}

// TestFindBundleDir locates target/release/bundle at the crate dir and, failing
// that, walks up to the workspace root (cargo's shared target dir).
func TestFindBundleDir(t *testing.T) {
	root := t.TempDir()
	crate := filepath.Join(root, "src-tauri")
	if err := os.MkdirAll(crate, 0o755); err != nil {
		t.Fatal(err)
	}

	// Not present anywhere yet.
	if got := findBundleDir(crate, root); got != "" {
		t.Errorf("expected no bundle dir, got %q", got)
	}

	// Workspace-root target dir (shared) is found by walking up.
	wsBundle := filepath.Join(root, "target", "release", "bundle")
	if err := os.MkdirAll(wsBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findBundleDir(crate, root); got != wsBundle {
		t.Errorf("findBundleDir = %q, want workspace bundle %q", got, wsBundle)
	}

	// A crate-local target dir takes precedence.
	crateBundle := filepath.Join(crate, "target", "release", "bundle")
	if err := os.MkdirAll(crateBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findBundleDir(crate, root); got != crateBundle {
		t.Errorf("findBundleDir = %q, want crate-local %q", got, crateBundle)
	}
}
