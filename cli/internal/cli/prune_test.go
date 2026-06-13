package cli

import (
	"slices"
	"testing"
)

func TestPruneCmdWiring(t *testing.T) {
	cmd := newPruneCmd()

	// Flag parsing must be off so flags like -n/--gone/--base reach clauderig
	// instead of colliding with rig's own flags or erroring as unknown.
	if !cmd.DisableFlagParsing {
		t.Error("prune passthrough must set DisableFlagParsing to forward flags")
	}
	if got := cmd.Name(); got != "prune" {
		t.Errorf("Name() = %q, want \"prune\"", got)
	}
	if !slices.Contains(cmd.Aliases, "tidy") {
		t.Errorf("Aliases = %v, want to contain \"tidy\"", cmd.Aliases)
	}
}
