package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveReleaseConfig finds the release config across its alternate locations
// (incl. a .rig.json key) and errors when more than one exists.
func TestResolveReleaseConfig(t *testing.T) {
	mk := func(t *testing.T) (root, cs string) {
		root = t.TempDir()
		cs = filepath.Join(root, ".changeset")
		if err := os.MkdirAll(cs, 0o755); err != nil {
			t.Fatal(err)
		}
		return root, cs
	}

	// None → empty (defaults) config, no error.
	root, cs := mk(t)
	if cfg, err := resolveReleaseConfig(root, cs); err != nil || cfg == nil {
		t.Fatalf("no config should yield defaults: cfg=%v err=%v", cfg, err)
	}

	// Alternate file: .changeset/shiprig.jsonc.
	root, cs = mk(t)
	if err := os.WriteFile(filepath.Join(cs, "shiprig.jsonc"), []byte(`{"tool":"shiprig"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err := resolveReleaseConfig(root, cs); err != nil || cfg.Tool != "shiprig" {
		t.Fatalf("alternate file: cfg=%+v err=%v", cfg, err)
	}

	// A "release" key in .rig.json.
	root, cs = mk(t)
	if err := os.WriteFile(filepath.Join(root, ".rig.json"), []byte(`{"release":{"tool":"shiprig"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err := resolveReleaseConfig(root, cs); err != nil || cfg.Tool != "shiprig" {
		t.Fatalf(".rig.json release key: cfg=%+v err=%v", cfg, err)
	}

	// Two sources → ambiguous.
	root, cs = mk(t)
	if err := os.WriteFile(filepath.Join(cs, "release.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shiprig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveReleaseConfig(root, cs); err == nil || !strings.Contains(err.Error(), "multiple release config") {
		t.Fatalf("two release configs should be ambiguous, got %v", err)
	}
}
