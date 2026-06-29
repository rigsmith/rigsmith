package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A project beside a velopack file is overlaid by the velopack ecosystem for the
// release path; it must NOT show up as a second dev target. Before the overlay
// skip, velopack re-emitted the base project under its own ecosystem id, and
// because topoSort keys by name the overlay copy (which maps no `run` verb)
// shadowed the base — so `rig run` couldn't run the project and a configured
// defaultProject naming it "didn't match a runnable project".
func TestDiscoverWorkspace_SkipsVelopackOverlay(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A runnable Go app at the module root with a velopack overlay beside it.
	writeGoPkg(t, root, ".", "main")
	if err := os.WriteFile(filepath.Join(root, "velopack.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := discoverWorkspace(context.Background(), root, nil)

	// The overlay contributes no dev target: only the base (go) owns the unit.
	for _, tg := range ts {
		if tg.Eco == "velopack" {
			t.Fatalf("velopack overlay leaked a dev target: %+v (all: %+v)", tg, ts)
		}
	}

	// And the base project resolves to exactly one runnable run target — not two
	// that collapse by name in topoSort.
	got := runTargets(context.Background(), root)
	n := 0
	for _, tg := range got {
		if isRunnable(tg) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("runnable run targets = %d, want exactly 1: %+v", n, got)
	}
}
