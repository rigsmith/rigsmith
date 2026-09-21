package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

// writePkg writes a generated package.json the way build-packages.mjs does.
func writePkg(t *testing.T, dir, name, version string, private bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"name": name, "version": version}
	if private {
		m["private"] = true
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// nodeCfg is a config whose node block carries the given publishDirs globs.
func nodeCfg(t *testing.T, globs ...string) *config.Config {
	t.Helper()
	raw := map[string]any{"node": map[string]any{"publishDirs": globs}}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(b)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func TestGeneratedPackagesExpandsGlobs(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	writePkg(t, filepath.Join(root, "npm", "dist", "rig-darwin-arm64"), "@rigsmith/rig-darwin-arm64", "1.19.0", false)

	pkgs, ecoOf, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d package(s), want 2: %+v", len(pkgs), pkgs)
	}
	for _, p := range pkgs {
		if p.Version != "1.19.0" {
			t.Errorf("%s: version %q, want 1.19.0", p.Name, p.Version)
		}
		if ecoOf[p.Name] != "node" {
			t.Errorf("%s: ecosystem %q, want node", p.Name, ecoOf[p.Name])
		}
		if !strings.HasPrefix(p.Dir, "npm/dist/") {
			t.Errorf("%s: dir %q should be repo-relative under npm/dist", p.Name, p.Dir)
		}
		if p.ManifestPath != p.Dir+"/package.json" {
			t.Errorf("%s: manifest %q, want %s/package.json", p.Name, p.ManifestPath, p.Dir)
		}
	}
}

// The directories do not exist until the build that writes them has run, so a
// glob matching nothing is the correct state before that — not a failure. This
// is the case that decides whether `shiprig publish` on a fresh clone works or
// dies.
func TestGeneratedPackagesMissingDirIsNotAnError(t *testing.T) {
	root := t.TempDir()
	pkgs, _, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatalf("a glob matching nothing must not be an error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("got %d package(s), want 0", len(pkgs))
	}
}

// A generated directory carrying a name discovery already found must not be
// published twice — two uploads of one name racing the registry.
func TestGeneratedPackagesSkipsNamesDiscoveryAlreadyFound(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	writePkg(t, filepath.Join(root, "npm", "dist", "shiprig"), "@rigsmith/shiprig", "1.19.0", false)

	known := map[string]bool{"@rigsmith/rig": true}
	pkgs, _, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), known)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "@rigsmith/shiprig" {
		t.Fatalf("got %+v, want only @rigsmith/shiprig", pkgs)
	}
}

// A glob like npm/dist/* also matches whatever else the generator left beside
// the packages; a directory with no package.json is simply not one.
func TestGeneratedPackagesIgnoresDirsWithoutAManifest(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	if err := os.MkdirAll(filepath.Join(root, "npm", "dist", "tmp-staging"), 0o755); err != nil {
		t.Fatal(err)
	}

	pkgs, _, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d package(s), want 1: %+v", len(pkgs), pkgs)
	}
}

// Private survives into the package, because the publish path is what reads it:
// the node adapter skips a private package rather than pushing it.
func TestGeneratedPackagesCarriesPrivate(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "internal"), "@rigsmith/internal", "1.19.0", true)

	pkgs, _, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || !pkgs[0].Private {
		t.Fatalf("got %+v, want one private package", pkgs)
	}
}

// A manifest with no name or no version is a generator bug. Publishing it would
// fail at the registry with a worse message, so it fails here with a path.
func TestGeneratedPackagesRejectsManifestMissingNameOrVersion(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no name", `{"version":"1.19.0"}`},
		{"no version", `{"name":"@rigsmith/rig"}`},
		{"malformed", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "npm", "dist", "rig")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
			if err == nil {
				t.Fatal("want an error naming the manifest, got nil")
			}
			if !strings.Contains(err.Error(), "npm/dist/rig/package.json") {
				t.Errorf("error should name the manifest, got: %v", err)
			}
		})
	}
}

// No publishDirs anywhere is the overwhelmingly common case: it must cost
// nothing and touch no disk.
func TestGeneratedPackagesNoConfigIsNoOp(t *testing.T) {
	cfg, err := config.Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, ecoOf, err := generatedPackages(t.TempDir(), cfg, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 0 || len(ecoOf) != 0 {
		t.Fatalf("got %+v / %+v, want empty", pkgs, ecoOf)
	}
}

// Only package.json is implemented. Ignoring the key for another ecosystem
// would be indistinguishable from "the build produced nothing" — the one state
// this deliberately treats as success — so it is an error instead.
func TestGeneratedPackagesRefusesUnimplementedEcosystem(t *testing.T) {
	raw := []byte(`{"cargo":{"publishDirs":["target/pkg/*"]}}`)
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = generatedPackages(t.TempDir(), cfg, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "cargo.publishDirs") {
		t.Fatalf("want an error naming cargo.publishDirs, got: %v", err)
	}
}
