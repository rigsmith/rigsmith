package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// Bare `shiprig` routes to the registered `ui` subcommand (not a standalone
// NewUICmd), so the shared menu's title — derived from cmd.Root().Name() —
// resolves to "shiprig" rather than "ui". Guards against regressing to a
// detached command whose Root() is itself.
func TestUISubcommandResolvesToolName(t *testing.T) {
	root := newRootCmd()
	var ui *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "ui" {
			ui = c
			break
		}
	}
	if ui == nil {
		t.Fatal("no ui subcommand registered on the root")
	}
	if got := ui.Root().Name(); got != "shiprig" {
		t.Fatalf("ui.Root().Name() = %q, want %q (menu title would be wrong)", got, "shiprig")
	}
}
