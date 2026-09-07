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
func TestHiddenWorktreesUnreadableIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "worktrees")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	r, ok := checkHiddenWorktrees(Env{RepoRoot: root})
	if !ok {
		t.Fatal("an unreadable .claude/worktrees produced no row at all")
	}
	if r.Status != Warn || !strings.Contains(r.Detail, "could not read") {
		t.Errorf("status=%v detail=%q, want a warning that says it could not look", r.Status, r.Detail)
	}
}
