package cli

import (
	"bytes"
	"context"
	"github.com/rigsmith/rigsmith/core/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackPackTargets(t *testing.T) {
	m := owned("app", "lib")

	t.Run("no argument packs every member", func(t *testing.T) {
		got, err := stackPackTargets(m, nil)
		if err != nil || len(got) != 2 {
			t.Fatalf("got (%v, %v), want both members", got, err)
		}
	})

	t.Run("a named member packs only that one", func(t *testing.T) {
		got, err := stackPackTargets(m, []string{"lib"})
		if err != nil || len(got) != 1 || got[0] != "lib" {
			t.Fatalf("got (%v, %v), want [lib]", got, err)
		}
	})

	t.Run("an unknown member names what there is", func(t *testing.T) {
		_, err := stackPackTargets(m, []string{"nope"})
		if err == nil || !strings.Contains(err.Error(), "app") {
			t.Fatalf("err = %v, want it to list the members", err)
		}
	})
}

func TestStackPackUnder(t *testing.T) {
	for _, tc := range []struct {
		member, dir string
		want        bool
	}{
		{"lib", "lib", true},
		{"lib", "lib/src/Lib", true},
		{"lib", "libextra/src", false}, // prefix, not a directory boundary
		{"lib", "app/src", false},
	} {
		if got := stackPackUnder(tc.member, tc.dir); got != tc.want {
			t.Errorf("stackPackUnder(%q, %q) = %v, want %v", tc.member, tc.dir, got, tc.want)
		}
	}
}

// The listing is what appeared on disk, not what the adapter predicted: the
// .NET adapter names the file <PackageId>.<Version>.nupkg from the version it
// was handed, and a project versioned at build time (MinVer) is discovered with
// none — so the predicted name carries an empty version and matches no file.
func TestStackPackNewFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Old.1.0.0.nupkg")
	before := stackPackDirState(dir)
	write("Mermaider.0.1.0-canary.0.23.nupkg")
	write("Sugiyama.0.1.0-canary.0.23.nupkg")

	got := stackPackNewFiles(dir, before)
	if len(got) != 2 || got[0] != "Mermaider.0.1.0-canary.0.23.nupkg" || got[1] != "Sugiyama.0.1.0-canary.0.23.nupkg" {
		t.Fatalf("new files = %v, want the two that appeared, sorted", got)
	}
}

// Packing without the overlay produces exactly the packages a bare checkout
// would, which is the situation the command exists to replace — so it refuses
// rather than handing back artifacts that are wrong in a way nothing
// downstream can detect.
func TestStackPackRequireOverlay(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses while the overlay is missing", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "app/src/App", "Acme.App", "Acme.Lib")
		csproj(t, root, "lib/src/Lib", "Acme.Lib")

		err := stackPackRequireOverlay(ctx, root, owned("app", "lib"))
		if err == nil {
			t.Fatal("packed with no overlay written")
		}
		if !strings.Contains(err.Error(), "resolve their siblings from a registry") {
			t.Fatalf("the error does not say what would go wrong: %v", err)
		}
	})

	t.Run("allows it when nothing crosses between members", func(t *testing.T) {
		// No links means no overlay is needed, and an overlay report about
		// tidiness is not a reason to refuse a build.
		root := t.TempDir()
		csproj(t, root, "app/src/App", "Acme.App")
		csproj(t, root, "lib/src/Lib", "Acme.Lib")

		if err := stackPackRequireOverlay(ctx, root, owned("app", "lib")); err != nil {
			t.Fatalf("refused with nothing crossing: %v", err)
		}
	})
}

func TestStackPackSaysWhenThereIsNothingToPack(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := stackPack(context.Background(), &b, root, []string{"lib"}, filepath.Join(root, "dist"), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "nothing publishable") {
		t.Fatalf("output = %q, want it to say there was nothing to pack", b.String())
	}
}

// Overlays means the desktop adapter owns that unit's artifacts and the base
// package for the same directory is dropped. Keeping the base and skipping the
// owner — which is what a naive copy of the link-scan's guard does — npm-packs
// an Electron app instead of building its installers.
func TestStackPackReconcileOverlays(t *testing.T) {
	base := stackPackPackage{info: plugin.EcosystemInfo{ID: "node"}, pkg: plugin.Package{Name: "app", Dir: "app"}}
	owner := stackPackPackage{info: plugin.EcosystemInfo{ID: "electron", Overlays: []string{"node"}}, pkg: plugin.Package{Name: "app", Dir: "app"}}
	other := stackPackPackage{info: plugin.EcosystemInfo{ID: "node"}, pkg: plugin.Package{Name: "lib", Dir: "lib"}}

	got := stackPackReconcileOverlays([]stackPackPackage{base, owner, other})
	if len(got) != 2 {
		t.Fatalf("kept %d packages, want the overlay owner and the unclaimed one", len(got))
	}
	for _, g := range got {
		if g.pkg.Dir == "app" && g.info.ID != "electron" {
			t.Errorf("kept the base package for app/, want the overlay that owns it")
		}
	}
}

// The nested case: cargo writes its .crate under OutputDir/package/, so a flat
// scan reports a successful build as having produced nothing.
func TestStackPackNewFilesFindsNestedOutput(t *testing.T) {
	dir := t.TempDir()
	before := stackPackDirState(dir)
	nested := filepath.Join(dir, "package")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "sugiyama-0.1.0.crate"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := stackPackNewFiles(dir, before)
	if len(got) != 1 || got[0] != "package/sugiyama-0.1.0.crate" {
		t.Fatalf("new files = %v, want the nested crate", got)
	}
}
