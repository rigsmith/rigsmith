package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worktree under .claude/worktrees is invisible where anyone would look for
// one — `rig worktree list` does not show them and `rig prune` does not reap
// them — so the doctor names them. Absent is the normal case and gets no row at
// all, rather than a green one nobody needs.
func TestHiddenWorktreesCheck(t *testing.T) {
	root := t.TempDir()

	if _, ok := checkHiddenWorktrees(Env{RepoRoot: root}); ok {
		t.Error("reported a row with no .claude/worktrees directory")
	}

	// An empty directory is not a finding either: something made it and left.
	if err := os.MkdirAll(filepath.Join(root, ".claude", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := checkHiddenWorktrees(Env{RepoRoot: root}); ok {
		t.Error("reported a row for an empty .claude/worktrees")
	}

	for _, name := range []string{"winget-pkgs-requirement-f7a732", "another-one"} {
		if err := os.MkdirAll(filepath.Join(root, ".claude", "worktrees", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r, ok := checkHiddenWorktrees(Env{RepoRoot: root})
	if !ok {
		t.Fatal("two worktrees present and nothing reported")
	}
	if r.Status != Warn {
		t.Errorf("status = %v, want Warn", r.Status)
	}
	if !strings.Contains(r.Detail, "winget-pkgs-requirement-f7a732") {
		t.Errorf("detail does not name what it found: %q", r.Detail)
	}
	// Named, not removed: a worktree can hold uncommitted work, and a doctor
	// that deletes one is worse than one that points at it.
	if r.Fix != nil {
		t.Error("the check offers to delete a worktree; it should only report")
	}
	if !strings.Contains(r.Hint, "git worktree remove") {
		t.Errorf("the hint does not say how to deal with them: %q", r.Hint)
	}
}

// A directory the check cannot read is not a directory with nothing in it.
// Swallowing the error would report clean when the truth is unknown.
// A directory that cannot be read is not an absent one: reporting clean because
// the check could not look is the answer most likely to be hiding something.
//
// Made unreadable by putting a file where the directory belongs rather than by
// chmod: Windows honours neither a mode of 0 on a directory nor Geteuid, so the
// permissions version of this test passed there by not reproducing the state at
// all. Any error that is not IsNotExist takes the same branch.
func TestHiddenWorktreesUnreadableIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "worktrees"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, ok := checkHiddenWorktrees(Env{RepoRoot: root})
	if !ok {
		t.Fatal("a .claude/worktrees that could not be read produced no row at all")
	}
	if r.Status != Warn || !strings.Contains(r.Detail, "could not read") {
		t.Errorf("status=%v detail=%q, want a warning that says it could not look", r.Status, r.Detail)
	}
}
