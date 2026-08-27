package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// refuseUnknownVerb makes a command group reject a verb it does not have.
//
// A group whose parent has a RunE — to open a picker on a terminal, or print
// help otherwise — swallows an unrecognised subcommand: cobra hands the argument
// to the parent, which shows the menu and returns nil. The result is that a verb
// this build has never heard of reports success and does nothing, which is worst
// of all for someone running an older rig against a newer script: `rig stack
// wire` on a build that predates it exits 0 and wires nothing.
//
// The no-argument behaviour is left exactly as it was — that is the picker, and
// it is right.
func refuseUnknownVerb(cmd *cobra.Command) *cobra.Command {
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return run(c, args)
		}
		have := make([]string, 0, len(c.Commands()))
		for _, sub := range c.Commands() {
			if !sub.Hidden && sub.Name() != "help" {
				have = append(have, sub.Name())
			}
		}
		sort.Strings(have)
		// Naming the version is not noise here: an old binary is a likelier
		// cause than a typo, and it is the one people do not think to check.
		return fmt.Errorf("%s has no %q — try one of: %s\n"+
			"if you expected it, this build may predate it: `rig --version`",
			c.CommandPath(), args[0], strings.Join(have, ", "))
	}
	return cmd
}
