package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/ecosystem"
)

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
