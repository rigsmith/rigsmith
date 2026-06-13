package cli

import (
	"slices"
	"testing"
)

func TestWorktreeCmdWiring(t *testing.T) {
	cmd := newWorktreeCmd()

	// Flag parsing must be off so flags like -n/--base reach clauderig instead
	// of colliding with rig's own --dry-run or erroring as unknown flags.
	if !cmd.DisableFlagParsing {
		t.Error("worktree passthrough must set DisableFlagParsing to forward flags")
	}
	if got := cmd.Name(); got != "worktree" {
		t.Errorf("Name() = %q, want \"worktree\"", got)
	}
	if !slices.Contains(cmd.Aliases, "wt") {
		t.Errorf("Aliases = %v, want to contain \"wt\"", cmd.Aliases)
	}
}
