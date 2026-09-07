package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// Unix only, and not for want of trying. Windows honours neither a mode of 0 on
// a directory nor Geteuid, and a file put where the directory belongs is no
// help either: ReadDir there enumerates with a trailing wildcard, so a
// non-directory comes back as ERROR_PATH_NOT_FOUND — IsNotExist, the branch
// this is not about. The logic under test has nothing platform-specific in it.
func TestHiddenWorktreesUnreadableIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no portable way to make a directory unreadable here")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "worktrees")
	if err := os.MkdirAll(dir, 0o700); err != nil {
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

// The hidden worktrees are made under the primary's .claude, and doctor is
// almost always run from somewhere else — working in a sibling worktree is the
// discipline this enforces. Anchoring the scan on the current checkout looked
// inside a linked one and found nothing, in the case that is the norm.
func TestHiddenWorktreesFoundFromALinkedCheckout(t *testing.T) {
	primary := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = primary
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(primary, "f"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")

	// One hidden worktree under the primary, and an ordinary sibling to stand in.
	hidden := filepath.Join(primary, ".claude", "worktrees", "agent-a")
	run("worktree", "add", "-q", "-b", "agent-a", hidden)
	sibling := filepath.Join(t.TempDir(), "side")
	run("worktree", "add", "-q", "-b", "side", sibling)

	for _, from := range []struct{ name, root string }{
		{"the primary", primary},
		{"a sibling worktree", sibling},
		{"the hidden worktree itself", hidden},
	} {
		r, ok := checkHiddenWorktrees(Env{RepoRoot: from.root})
		if !ok {
			t.Errorf("from %s: no row — the hidden worktree went unreported", from.name)
			continue
		}
		if !strings.Contains(r.Detail, "agent-a") {
			t.Errorf("from %s: detail = %q, want it to name agent-a", from.name, r.Detail)
		}
	}
}
