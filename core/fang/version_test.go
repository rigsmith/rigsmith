package fang

import (
	"strings"
	"testing"
)

// With RIGSMITH_DEV_SRC set (as the -dev/-wt launchers do), the source location
// is that worktree path verbatim.
func TestSourceLocationUsesDevSrc(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "/tmp/rigsmith-worktrees/feat-x")
	if got := sourceLocation(); got != "/tmp/rigsmith-worktrees/feat-x" {
		t.Errorf("sourceLocation() = %q, want the RIGSMITH_DEV_SRC path", got)
	}
}

// Without it, the location falls back to the binary's own path (never empty in a
// normal run).
func TestSourceLocationFallsBackToExe(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "")
	if got := sourceLocation(); got == "" {
		t.Error("sourceLocation() should fall back to the executable path")
	}
}

// A versionless build is described, not left as a bare "unknown": it names the
// build and its source location (here, the dev worktree).
func TestSourceBuildVersionDescribesTheBuild(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "/tmp/rigsmith-worktrees/feat-x")
	got := sourceBuildVersion(nil)
	if !strings.HasPrefix(got, "source build") {
		t.Errorf("version = %q, want it to start with \"source build\"", got)
	}
	if !strings.Contains(got, "/tmp/rigsmith-worktrees/feat-x") {
		t.Errorf("version = %q, want it to include the source location", got)
	}
}
