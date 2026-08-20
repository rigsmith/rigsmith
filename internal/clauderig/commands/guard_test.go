package commands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/guard"
)

// Covers the FULL decide path, not just the pure engine — because that is where
// the leak was. Monitor reached Evaluate correctly and still could not be
// denied: the staged-file lookup that base-branch commit protection depends on
// was gated on the literal tool name "Bash", so a Monitor commit arrived with an
// empty file list and deferred. Both tools must behave identically here.
func TestDecide_MonitorCannotCommitToABaseBranch(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	// Staged CODE — a doc would be low-risk and allowed on a base branch.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")

	t.Setenv("CLAUDERIG_ALLOW_MAIN", "")
	for _, tool := range []string{"Bash", "Monitor"} {
		payload := []byte(`{"tool_name":"` + tool + `","cwd":"` + filepath.ToSlash(dir) + `","tool_input":{"command":"git commit -m x"}}`)
		if got := decide(context.Background(), payload); got.Decision != guard.Deny {
			t.Errorf("%s committing main.go on main: decision = %v, want Deny", tool, got.Decision)
		}
	}
}
