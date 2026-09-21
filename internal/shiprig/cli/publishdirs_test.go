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

	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Packages) != 2 {
		t.Fatalf("got %d package(s), want 2: %+v", len(gen.Packages), gen.Packages)
	}
	for _, p := range gen.Packages {
		if p.Version != "1.19.0" {
			t.Errorf("%s: version %q, want 1.19.0", p.Name, p.Version)
		}
		if gen.Eco[p.Name] != "node" {
			t.Errorf("%s: ecosystem %q, want node", p.Name, gen.Eco[p.Name])
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
	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatalf("a glob matching nothing must not be an error: %v", err)
	}
	if len(gen.Packages) != 0 {
		t.Fatalf("got %d package(s), want 0", len(gen.Packages))
	}
}

// A generated directory carrying a name discovery already found must not be
// published twice — two uploads of one name racing the registry.
func TestGeneratedPackagesSkipsNamesDiscoveryAlreadyFound(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	writePkg(t, filepath.Join(root, "npm", "dist", "shiprig"), "@rigsmith/shiprig", "1.19.0", false)

	known := map[string]bool{"@rigsmith/rig": true}
	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), known)
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Packages) != 1 || gen.Packages[0].Name != "@rigsmith/shiprig" {
		t.Fatalf("got %+v, want only @rigsmith/shiprig", gen.Packages)
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

	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Packages) != 1 {
		t.Fatalf("got %d package(s), want 1: %+v", len(gen.Packages), gen.Packages)
	}
}

// Private survives into the package, because the publish path is what reads it:
// the node adapter skips a private package rather than pushing it.
func TestGeneratedPackagesCarriesPrivate(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "internal"), "@rigsmith/internal", "1.19.0", true)

	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Packages) != 1 || !gen.Packages[0].Private {
		t.Fatalf("got %+v, want one private package", gen.Packages)
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
			_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
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
	gen, err := generatedPackages(t.TempDir(), cfg, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Packages) != 0 || len(gen.Eco) != 0 {
		t.Fatalf("got %+v / %+v, want empty", gen.Packages, gen.Eco)
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
	_, err = generatedPackages(t.TempDir(), cfg, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "cargo.publishDirs") {
		t.Fatalf("want an error naming cargo.publishDirs, got: %v", err)
	}
}

// A glob is repo-relative by contract. "../elsewhere/*" would join into a real
// directory outside the tree, and each package built from it becomes a working
// directory the publisher runs npm in — so it is refused before expansion,
// which also means it is refused whether or not the target exists.
func TestGeneratedPackagesRefusesPatternsThatLeaveTheRepo(t *testing.T) {
	for _, pattern := range []string{
		"../other-repo/*",
		"npm/../../escape/*",
		"/etc/*",
		"./../../*",
	} {
		t.Run(pattern, func(t *testing.T) {
			_, err := generatedPackages(t.TempDir(), nodeCfg(t, pattern), map[string]bool{})
			if err == nil {
				t.Fatalf("%q escapes the repository and must be refused", pattern)
			}
			if !strings.Contains(err.Error(), "repo-relative") {
				t.Errorf("the error should say the pattern must be repo-relative, got: %v", err)
			}
		})
	}
}

// A local pattern can still match a symlink pointing out of the tree, which
// os.Stat happily follows.
func TestGeneratedPackagesRefusesSymlinkOutOfTheRepo(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writePkg(t, filepath.Join(outside, "evil"), "@rigsmith/evil", "1.19.0", false)
	if err := os.MkdirAll(filepath.Join(root, "npm", "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(root, "npm", "dist", "evil")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err == nil {
		t.Fatal("a symlink resolving outside the repository must be refused")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("the error should say it resolves outside the repository, got: %v", err)
	}
}

// The workspace-wide `access` describes the packages in the tree. These wrappers
// are scoped npm packages that must go out public from a repo configured
// "restricted", and each manifest says so via npm's own publishConfig.access.
// Publishing them at the workspace default would publish them privately.
func TestGeneratedPackagesCarriesDeclaredAccess(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "npm", "dist", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"@rigsmith/rig","version":"1.19.0","publishConfig":{"access":"public"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writePkg(t, filepath.Join(root, "npm", "dist", "plain"), "plain-thing", "1.19.0", false)

	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if got := gen.Access["@rigsmith/rig"]; got != "public" {
		t.Errorf("declared access = %q, want public", got)
	}
	// A manifest that declares nothing must not invent an access: the caller
	// falls back to the workspace setting, and a value here would override it.
	if got, ok := gen.Access["plain-thing"]; ok {
		t.Errorf("a manifest declaring no access should record none, got %q", got)
	}
}

// npm understands "public" and "restricted". The adapter maps anything else to
// "restricted", so a typo would publish a package meant to be public privately
// and report success — refuse it where the manifest can still be named.
func TestGeneratedPackagesRejectsUnknownAccess(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "npm", "dist", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"@rigsmith/rig","version":"1.19.0","publishConfig":{"access":"pubic"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err == nil {
		t.Fatal("an access value npm does not understand must be refused")
	}
	for _, want := range []string{"npm/dist/rig/package.json", "pubic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
}

// EcoConfig degrades a malformed block to defaults on purpose, so a publishDirs
// written as a string rather than a list became an empty slice — and the run
// published none of the generated packages while reporting success. "Absent"
// and "unreadable" must not look the same for this key.
func TestGeneratedPackagesRefusesMalformedPublishDirs(t *testing.T) {
	for _, body := range []string{
		`{"node":{"publishDirs":"npm/dist/*"}}`,
		`{"node":{"publishDirs":{"glob":"npm/dist/*"}}}`,
		`{"node":{"publishDirs":[1,2]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			cfg, err := config.Parse([]byte(body))
			if err != nil {
				t.Skipf("config refused it earlier, which is also fine: %v", err)
			}
			_, err = generatedPackages(t.TempDir(), cfg, map[string]bool{})
			if err == nil {
				t.Fatal("a publishDirs that cannot be read as a list of globs must not silently " +
					"publish nothing")
			}
			if !strings.Contains(err.Error(), "publishDirs") {
				t.Errorf("the error should name the key, got: %v", err)
			}
		})
	}
}

// A match that cannot be inspected is not the same as one that is not a
// directory: treating them alike silently shrinks the set of packages
// published while the run reports success.
func TestGeneratedPackagesReportsUninspectableMatches(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can stat through a 0000 directory")
	}
	root := t.TempDir()
	dist := filepath.Join(root, "npm", "dist")
	writePkg(t, filepath.Join(dist, "rig"), "@rigsmith/rig", "1.19.0", false)
	// A dangling symlink matches the glob and cannot be stat'd.
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(dist, "ghost")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err == nil {
		t.Fatal("a match that cannot be inspected must be reported, not skipped")
	}
}

// The directory is checked, but the manifest inside it is a separate path and
// can be a symlink of its own pointing at a package.json outside the tree.
func TestGeneratedPackagesRefusesSymlinkedManifest(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "package.json"),
		[]byte(`{"name":"@rigsmith/evil","version":"9.9.9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "npm", "dist", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(dir, "package.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err == nil {
		t.Fatal("a manifest resolving outside the repository must be refused")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("the error should say it resolves outside the repository, got: %v", err)
	}
}

// Two generated directories claiming one package name is a build that produced
// it twice. Taking whichever sorted first would publish an arbitrary one.
func TestGeneratedPackagesReportsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	writePkg(t, filepath.Join(root, "npm", "dist", "rig-copy"), "@rigsmith/rig", "1.19.0", false)

	_, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err == nil {
		t.Fatal("two directories generating one package name must be reported")
	}
	for _, want := range []string{"@rigsmith/rig", "npm/dist/rig", "npm/dist/rig-copy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q, got: %v", want, err)
		}
	}
}

// Overlapping globs matching the SAME directory are harmless and must stay so.
func TestGeneratedPackagesAllowsOverlappingGlobs(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)

	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*", "npm/dist/r*"), map[string]bool{})
	if err != nil {
		t.Fatalf("two globs matching one directory is not a conflict: %v", err)
	}
	if len(gen.Packages) != 1 {
		t.Fatalf("got %d package(s), want 1", len(gen.Packages))
	}
}

// A configured glob matching nothing stays a non-error — before the build that
// writes those directories has run, empty is correct. But it must not be
// silent: a publish that shipped none of the generated packages otherwise looks
// exactly like one that had none to ship.
func TestGeneratedPackagesNotesWhenConfiguredGlobsMatchNothing(t *testing.T) {
	gen, err := generatedPackages(t.TempDir(), nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatalf("a glob matching nothing must not be an error: %v", err)
	}
	if len(gen.Packages) != 0 {
		t.Fatalf("got %d package(s), want 0", len(gen.Packages))
	}
	if len(gen.Notes) != 1 {
		t.Fatalf("want one note saying nothing matched, got %v", gen.Notes)
	}
	for _, want := range []string{"node.publishDirs", "npm/dist/*", "matched no packages"} {
		if !strings.Contains(gen.Notes[0], want) {
			t.Errorf("the note should mention %q, got: %s", want, gen.Notes[0])
		}
	}
}

// No publishDirs configured means nothing to say — the note exists for the
// difference between "configured and empty" and "not configured".
func TestGeneratedPackagesSaysNothingWhenNotConfigured(t *testing.T) {
	cfg, err := config.Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := generatedPackages(t.TempDir(), cfg, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Notes) != 0 {
		t.Errorf("no publishDirs configured should produce no notes, got %v", gen.Notes)
	}
}

// And a glob that DID match says nothing, so the note stays meaningful.
func TestGeneratedPackagesSaysNothingWhenGlobsMatch(t *testing.T) {
	root := t.TempDir()
	writePkg(t, filepath.Join(root, "npm", "dist", "rig"), "@rigsmith/rig", "1.19.0", false)
	gen, err := generatedPackages(root, nodeCfg(t, "npm/dist/*"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gen.Notes) != 0 {
		t.Errorf("a glob that matched should produce no notes, got %v", gen.Notes)
	}
}
