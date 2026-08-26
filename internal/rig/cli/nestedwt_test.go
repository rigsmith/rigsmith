package cli

import (
	"io"
	"testing"

	"github.com/rigsmith/rigsmith/core/walkutil"
)

// The flag has to reach the walker, not only the filter after it: discovery
// prunes worktrees before rig's own targets exist, so a bridge that failed to
// fire — or failed to clear on the next default invocation — would make
// `--include-worktrees` silently do nothing.
func TestIncludeWorktreesFlagReachesTheWalker(t *testing.T) {
	t.Cleanup(func() {
		includeWorktrees = false
		walkutil.SetIncludeWorktrees(false)
	})

	root := newRootCmd()
	root.SetArgs([]string{"--include-worktrees", "info"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_ = root.Execute()
	if !walkutil.IncludeWorktrees() {
		t.Fatal("--include-worktrees did not reach walkutil")
	}

	// A later default invocation must clear it, or one flagged run leaks the
	// opt-in into every command after it in the same process.
	includeWorktrees = false
	root2 := newRootCmd()
	root2.SetArgs([]string{"info"})
	root2.SetOut(io.Discard)
	root2.SetErr(io.Discard)
	_ = root2.Execute()
	if walkutil.IncludeWorktrees() {
		t.Fatal("the opt-in leaked into a later default invocation")
	}
}
