package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverWorkspaceExcludes: discoverWorkspace drops packages matching the
// exclude globs (by full or short name), keeping the pickers consistent with info.
func TestDiscoverWorkspaceExcludes(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, filepath.Join(root, "core"), "example.com/core")
	writeGoMod(t, filepath.Join(root, "bench"), "example.com/bench")

	// Unfiltered: both discovered.
	if got := discoverWorkspace(context.Background(), root, nil); len(got) != 2 {
		t.Fatalf("no exclude: got %d targets, want 2: %v", len(got), names(got))
	}

	// Exclude by short name glob.
	got := discoverWorkspace(context.Background(), root, []string{"*bench"})
	if len(got) != 1 || got[0].Name != "example.com/core" {
		t.Errorf("exclude *bench = %v, want only example.com/core", names(got))
	}

	// Exclude by full name.
	got = discoverWorkspace(context.Background(), root, []string{"example.com/core"})
	if len(got) != 1 || got[0].Name != "example.com/bench" {
		t.Errorf("exclude full name = %v, want only example.com/bench", names(got))
	}
}
