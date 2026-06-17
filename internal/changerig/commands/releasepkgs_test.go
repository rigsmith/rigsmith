package commands

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/ecosystem"
)

// TestReleasePackages: a released-since-tag feat shows the package as releasing;
// adding it to the ignore list flips it to Ignored and out of the plan.
func TestReleasePackages(t *testing.T) {
	dir := initGoRepo(t, "example.com/a", "v1.0.0")
	writeF(t, filepath.Join(dir, "feature.go"), "package main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "feat: add a feature")

	cfg, err := config.Parse([]byte(`{"versioning":{"source":"commits"}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: cfg, Registry: ecosystem.Default()}

	rps, err := ReleasePackages(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rps) != 1 {
		t.Fatalf("got %d packages, want 1: %+v", len(rps), rps)
	}
	p := rps[0]
	if p.Name != "example.com/a" || !p.Releasing() || p.Next != "1.1.0" {
		t.Errorf("got %+v, want example.com/a releasing → 1.1.0", p)
	}
	if p.Ignored {
		t.Error("package should not be ignored")
	}

	// Ignore it → no longer releasing, marked Ignored, still listed.
	ws.Config.Ignore = []string{"example.com/a"}
	rps, err = ReleasePackages(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rps) != 1 || !rps[0].Ignored || rps[0].Releasing() {
		t.Fatalf("ignored package should be listed, Ignored, not releasing: %+v", rps)
	}
}

// TestWriteIgnoreRoundTrip writes the ignore list (deduped + sorted) into a
// fresh changeset config and reads it back through config.Load.
func TestWriteIgnoreRoundTrip(t *testing.T) {
	dir := initGoRepo(t, "example.com/a", "v1.0.0")
	t.Chdir(dir) // WriteIgnore resolves the config relative to the working dir

	path, ok, err := WriteIgnore([]string{"*-demo", "pkg-b", "pkg-b", "  "})
	if err != nil || !ok {
		t.Fatalf("WriteIgnore: ok=%v err=%v", ok, err)
	}
	if filepath.Base(filepath.Dir(path)) != ".changeset" {
		t.Errorf("wrote to %s, want under .changeset/", path)
	}

	cfg, err := config.Load(filepath.Join(dir, ".changeset"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*-demo", "pkg-b"} // deduped + sorted, blank dropped
	if len(cfg.Ignore) != len(want) {
		t.Fatalf("ignore = %v, want %v", cfg.Ignore, want)
	}
	for i, w := range want {
		if cfg.Ignore[i] != w {
			t.Errorf("ignore[%d] = %q, want %q", i, cfg.Ignore[i], w)
		}
	}
	if !cfg.IsIgnored("anything-demo") {
		t.Error(`"*-demo" glob should match anything-demo`)
	}
}
