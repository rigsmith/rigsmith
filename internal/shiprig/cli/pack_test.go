package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// fakePacker is fakeRegistry that also builds: each package's "tarball" is
// its name and version as text, and files says how many package files to
// report (default one). built records what was built.
type fakePacker struct {
	fakeRegistry
	files    map[string]int
	built    *[]string
	checksum bool // also report a checksums file, which isn't a package
}

func (f fakePacker) Artifacts(_ context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	*f.built = append(*f.built, req.Package.Name)
	n, ok := f.files[req.Package.Name]
	if !ok {
		n = 1
	}
	var arts []plugin.Artifact
	for i := 0; i < n; i++ {
		p := filepath.Join(req.OutputDir, req.Package.Name+"-"+req.Package.Version+strings.Repeat("x", i)+".tgz")
		if err := os.WriteFile(p, []byte(req.Package.Name+"@"+req.Package.Version), 0o644); err != nil {
			return plugin.ArtifactsResponse{}, err
		}
		arts = append(arts, plugin.Artifact{Path: p, Kind: plugin.ArtifactPackage})
	}
	if f.checksum {
		p := filepath.Join(req.OutputDir, "checksums.txt")
		if err := os.WriteFile(p, []byte("sums"), 0o644); err != nil {
			return plugin.ArtifactsResponse{}, err
		}
		arts = append(arts, plugin.Artifact{Path: p, Kind: plugin.ArtifactChecksum})
	}
	return plugin.ArtifactsResponse{Built: true, Artifacts: arts}, nil
}

// packWith packs plan (nil: work it out) with the fake standing in for npm.
func packWith(t *testing.T, fake fakePacker, plan [][]planRelease) (string, [][]planRelease, error) {
	t.Helper()
	ws, err := commands.Open()
	if err != nil {
		t.Fatal(err)
	}
	node, _ := ws.EcosystemFor("node")
	fake.Ecosystem = node
	ws.Registry.Register(fake)
	if plan == nil {
		if plan, err = buildPublishPlan(context.Background(), ws, ""); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "pack")
	_, err = packPlan(context.Background(), ws, plan, out)
	if err != nil {
		return out, nil, err
	}
	written, err := readPublishPlan(filepath.Join(out, packDirPlan))
	if err != nil {
		t.Fatal(err)
	}
	return out, written, nil
}

func TestPackBuildsEachPublishReleaseAndRecordsIt(t *testing.T) {
	planRepo(t, `{ "privatePackages": { "version": true, "tag": true } }`)
	var built []string
	// The adapter reports a checksums file too; only the package file is packed.
	out, plan, err := packWith(t, fakePacker{fakeRegistry: fakeRegistry{published: map[string]bool{"done": true}}, built: &built, checksum: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(built, ",") != "lib,app" {
		t.Errorf("built %v, want lib then app (tag-only priv isn't built)", built)
	}
	for _, chunk := range plan {
		for _, r := range chunk {
			if r.Kind == "tag-only" {
				if r.Tarball != nil {
					t.Errorf("%s is tag-only but has a tarball", r.Name)
				}
				continue
			}
			if r.Tarball == nil {
				t.Fatalf("%s has no tarball", r.Name)
			}
			want := "packages/" + r.Name + "-" + r.Version + ".tgz"
			if r.Tarball.Path != want {
				t.Errorf("%s path = %q, want %q", r.Name, r.Tarball.Path, want)
			}
			data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(r.Tarball.Path)))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if r.Tarball.Integrity != "sha256-"+base64.StdEncoding.EncodeToString(sum[:]) {
				t.Errorf("%s integrity = %q, not the file's sha256", r.Name, r.Tarball.Integrity)
			}
		}
	}
}

// A plan from another commit, a cargo release, or an adapter that doesn't
// produce exactly one package file are all refused; the first two before
// anything is built.
func TestPackRefusals(t *testing.T) {
	planRepo(t, `{}`)
	var built []string
	_, _, err := packWith(t, fakePacker{built: &built}, [][]planRelease{{{Kind: "publish", Name: "lib", Version: "0.9.0", Ecosystem: "node"}}})
	if err == nil || !strings.Contains(err.Error(), "the plan has lib@0.9.0") {
		t.Errorf("stale plan: err = %v", err)
	}
	_, _, err = packWith(t, fakePacker{built: &built}, [][]planRelease{{{Kind: "publish", Name: "nope", Version: "1.0.0"}}})
	if err == nil || !strings.Contains(err.Error(), "isn't in this workspace") {
		t.Errorf("unknown package: err = %v", err)
	}
	if len(built) != 0 {
		t.Errorf("built %v before refusing", built)
	}
	_, _, err = packWith(t, fakePacker{built: &built, files: map[string]int{"lib": 2}}, [][]planRelease{{{Kind: "publish", Name: "lib", Version: "1.0.0"}}})
	if err == nil || !strings.Contains(err.Error(), "expected one package file") {
		t.Errorf("two files: err = %v", err)
	}
}

func TestReadPublishPlanChecksTheVersion(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]string{
		`{"version": 2, "plan": []}`: "version 2",
		`{"plan": []}`:               "version none",
		`{"version": 1}`:             "not a publish plan",
		`[]`:                         "not a publish plan",
	} {
		p := filepath.Join(dir, "plan.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readPublishPlan(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
}

// Cargo can't publish a prebuilt crate, so a cargo release is refused before
// anything is built.
func TestPackRefusesACargoRelease(t *testing.T) {
	root := planRepo(t, `{}`)
	if err := os.MkdirAll(filepath.Join(root, "crates", "tool", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"Cargo.toml":              "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n",
		"crates/tool/Cargo.toml":  "[package]\nname = \"tool\"\nversion = \"0.3.0\"\nedition = \"2021\"\n",
		"crates/tool/src/main.rs": "fn main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var built []string
	_, _, err := packWith(t, fakePacker{built: &built}, [][]planRelease{
		{{Kind: "publish", Name: "lib", Version: "1.0.0"}, {Kind: "publish", Name: "tool", Version: "0.3.0", Ecosystem: "cargo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cargo can't publish a prebuilt crate") {
		t.Fatalf("err = %v, want the cargo refusal", err)
	}
	if len(built) != 0 {
		t.Errorf("built %v before refusing", built)
	}
}

// packAndRead packs the planRepo workspace with the fake, then reads the pack
// directory back as publish --from-pack-dir does.
func packAndRead(t *testing.T, tamper func(dir string)) ([]packedRelease, error) {
	t.Helper()
	var built []string
	out, _, err := packWith(t, fakePacker{fakeRegistry: fakeRegistry{published: map[string]bool{"done": true}}, built: &built}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tamper != nil {
		tamper(out)
	}
	ws, err := commands.Open()
	if err != nil {
		t.Fatal(err)
	}
	pkgs, ecoOf, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return readPackDir(out, pkgs, ecoOf)
}

func TestReadPackDirReturnsThePlansFilesInOrder(t *testing.T) {
	planRepo(t, `{}`)
	releases, err := packAndRead(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range releases {
		got = append(got, r.pkg.Name+" "+filepath.Base(r.file)+" "+r.tag)
	}
	want := []string{"lib lib-1.0.0.tgz latest", "app app-2.0.0.tgz latest"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("releases = %v, want %v", got, want)
	}
}

func TestReadPackDirRefusals(t *testing.T) {
	// lib's entry, wherever it sits in the plan.
	lib := func(f *publishPlanFile) *planRelease {
		for ci := range f.Plan {
			for ri := range f.Plan[ci] {
				if f.Plan[ci][ri].Name == "lib" {
					return &f.Plan[ci][ri]
				}
			}
		}
		t.Fatal("no lib in the plan")
		return nil
	}
	rewrite := func(dir string, edit func(*publishPlanFile)) {
		plan, err := readPublishPlan(filepath.Join(dir, packDirPlan))
		if err != nil {
			t.Fatal(err)
		}
		f := publishPlanFile{Version: 1, Plan: plan}
		edit(&f)
		if err := writePublishPlan(filepath.Join(dir, packDirPlan), f.Plan); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		tamper func(dir string)
		want   string
	}{
		"a file changed after packing": {func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "packages", "lib-1.0.0.tgz"), []byte("swapped"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "doesn't match the integrity"},
		"a version other than the workspace's": {func(dir string) {
			rewrite(dir, func(f *publishPlanFile) { lib(f).Version = "0.9.0" })
		}, "publish from the commit that was packed"},
		"a path outside the pack directory": {func(dir string) {
			rewrite(dir, func(f *publishPlanFile) { lib(f).Tarball.Path = "../elsewhere.tgz" })
		}, "outside the pack directory"},
		"no file recorded": {func(dir string) {
			rewrite(dir, func(f *publishPlanFile) { lib(f).Tarball = nil })
		}, "no file for lib"},
	} {
		t.Run(name, func(t *testing.T) {
			planRepo(t, `{}`)
			_, err := packAndRead(t, tc.tamper)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
