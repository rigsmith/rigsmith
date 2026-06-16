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

// A directory glob ("sub/*") hides a project by its repo-relative path, not just
// by name — the lever behind the picker's "exclude the whole folder" option.
func TestDiscoverWorkspaceExcludes_ByPath(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, filepath.Join(root, "core"), "example.com/core")
	writeGoMod(t, filepath.Join(root, "examples", "demo"), "example.com/demo")

	got := discoverWorkspace(context.Background(), root, []string{"examples/*"})
	if len(got) != 1 || got[0].Name != "example.com/core" {
		t.Errorf("exclude examples/* = %v, want only example.com/core", names(got))
	}
}

func TestCrowdedExcludeDir(t *testing.T) {
	var all []string
	for i := 0; i < 6; i++ {
		all = append(all, "examples/p"+string(rune('a'+i)))
	}
	all = append(all, "cmd/rig", "cmd/clauderig", "rig-top")

	glob, dir, n, ok := crowdedExcludeDir("examples/pa", all)
	if !ok || glob != "examples/*" || dir != "examples" || n != 6 {
		t.Fatalf("crowdedExcludeDir(examples/pa) = (%q,%q,%d,%v), want (examples/*, examples, 6, true)", glob, dir, n, ok)
	}
	if _, _, _, ok := crowdedExcludeDir("cmd/rig", all); ok {
		t.Errorf("cmd/ with 2 projects (< threshold) should not offer a whole-dir exclude")
	}
	if _, _, _, ok := crowdedExcludeDir("rig-top", all); ok {
		t.Errorf("a top-level project should not offer a whole-dir exclude")
	}
}

func TestMatchingExcludes(t *testing.T) {
	patterns := []string{"examples/*", "changerig", "@acme/cli"}
	if got := matchingExcludes("app", "app", "examples/app", patterns); len(got) != 1 || got[0] != "examples/*" {
		t.Errorf("examples/app matched %v, want [examples/*]", got)
	}
	if got := matchingExcludes("changerig", "changerig", "cmd/changerig", patterns); len(got) != 1 || got[0] != "changerig" {
		t.Errorf("changerig matched %v, want [changerig]", got)
	}
	if got := matchingExcludes("rig", "rig", "cmd/rig", patterns); len(got) != 0 {
		t.Errorf("cmd/rig should not match any exclude, got %v", got)
	}
}

func TestPreciseExcludeGlob(t *testing.T) {
	names := []string{"rig", "server", "server"} // two "server" binaries
	if got := preciseExcludeGlob("rig", "cmd/rig", names); got != "rig" {
		t.Errorf("unique name should exclude by name, got %q", got)
	}
	if got := preciseExcludeGlob("server", "cmd/server", names); got != "cmd/server" {
		t.Errorf("colliding name should exclude by path, got %q", got)
	}
}

func TestProjectExcluded_NameShortAndPath(t *testing.T) {
	patterns := []string{"examples/*", "cli"}
	if !projectExcluded("app", "app", "examples/app", patterns) {
		t.Error("examples/* should hide examples/app by path")
	}
	if !projectExcluded("@acme/cli", "cli", "packages/cli", patterns) {
		t.Error("the 'cli' glob should hide @acme/cli by short name")
	}
	if projectExcluded("rig", "rig", "cmd/rig", patterns) {
		t.Error("cmd/rig must not be hidden")
	}
}
