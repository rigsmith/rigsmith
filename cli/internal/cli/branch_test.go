package cli

import (
	"slices"
	"testing"
)

func TestBranchCmdWiring(t *testing.T) {
	cmd := newBranchCmd()

	// Flag parsing must be off so flags like -n/--gone/--base reach clauderig
	// instead of colliding with rig's own flags or erroring as unknown.
	if !cmd.DisableFlagParsing {
		t.Error("branch passthrough must set DisableFlagParsing to forward flags")
	}
	if got := cmd.Name(); got != "branch" {
		t.Errorf("Name() = %q, want \"branch\"", got)
	}
	if !slices.Contains(cmd.Aliases, "br") {
		t.Errorf("Aliases = %v, want to contain \"br\"", cmd.Aliases)
	}
}
