package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdir creates a directory (and parents) and returns its path.
func mkdir(t *testing.T, elem ...string) string {
	t.Helper()
	dir := filepath.Join(elem...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Ported from the .NET rig's RootResolverTests: precedence is by category, not
// distance — .rig.json > solution/manifest > git root — and a .git ancestor
// bounds the walk.

func TestRoot_RigJsonWinsEvenOverACloserSolution(t *testing.T) {
	tmp := t.TempDir()
	// parent has .rig.json; child has a solution; start in a deeper dir.
	write(t, filepath.Join(tmp, ".rig.json"))
	write(t, filepath.Join(tmp, "child", "App.slnx"))
	start := mkdir(t, tmp, "child", "sub")

	if got := Root(start); got != tmp {
		t.Fatalf("Root(%q) = %q, want %q (.rig.json dir)", start, got, tmp)
	}
}

func TestRoot_FallsBackToNearestSolutionWhenNoConfig(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "repo", "App.sln"))
	start := mkdir(t, tmp, "repo", "src")

	want := filepath.Join(tmp, "repo")
	if got := Root(start); got != want {
		t.Fatalf("Root(%q) = %q, want %q (solution dir)", start, got, want)
	}
}

func TestRoot_FallsBackToGitRootWhenNoConfigOrSolution(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, tmp, ".git")
	start := mkdir(t, tmp, "a", "b")

	if got := Root(start); got != tmp {
		t.Fatalf("Root(%q) = %q, want %q (git root)", start, got, tmp)
	}
}

func TestRoot_AGitBoundaryStopsTheClimbSoASolutionAboveTheRepoIsIgnored(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "Outer.sln")) // outside the repo
	mkdir(t, tmp, "repo", ".git")             // the repo boundary
	start := mkdir(t, tmp, "repo", "src")

	want := filepath.Join(tmp, "repo")
	if got := Root(start); got != want {
		t.Fatalf("Root(%q) = %q, want %q (git boundary)", start, got, want)
	}
}

func TestRoot_ASolutionInsideTheRepoStillWinsOverTheGitRoot(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, tmp, ".git")
	write(t, filepath.Join(tmp, "src", "App.slnx")) // below the git root, in a subdir
	start := mkdir(t, tmp, "src", "sub")

	want := filepath.Join(tmp, "src")
	if got := Root(start); got != want {
		t.Fatalf("Root(%q) = %q, want %q (in-repo solution)", start, got, want)
	}
}

func TestRoot_DetectsGitWhenDotGitIsAFileWorktree(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, ".git")) // a worktree's .git is a file
	start := mkdir(t, tmp, "nested")

	if got := Root(start); got != tmp {
		t.Fatalf("Root(%q) = %q, want %q (.git file counts as a git marker)", start, got, tmp)
	}
}
