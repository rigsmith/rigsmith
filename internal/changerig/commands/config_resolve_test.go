package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A changeset config embedded in .rig.json resolves for read commands (show/get/
// path see it), while configFile — the write target for set/edit — refuses it
// and points the user at the .rig.json key.
func TestResolveConfigSource_InlineRigKey(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil { // anchors the workspace root
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rig.json"),
		[]byte(`{"changerig":{"baseBranch":"trunk"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	src, _, err := resolveConfigSource()
	if err != nil || src == nil {
		t.Fatalf("inline config should resolve: src=%+v err=%v", src, err)
	}
	if src.Path != "" {
		t.Errorf("inline source should have no file Path, got %q", src.Path)
	}
	if !strings.Contains(src.Origin, ".rig.json") {
		t.Errorf("Origin should name .rig.json, got %q", src.Origin)
	}

	// set/edit need a file → configFile errors and points at the .rig.json key.
	if _, err := configFile(); err == nil || !strings.Contains(err.Error(), ".rig.json") {
		t.Fatalf("configFile should refuse an inline config and name it: %v", err)
	}
}
