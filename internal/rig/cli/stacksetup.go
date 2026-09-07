package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newStackSetupCmd is the one command a fresh clone of a stackspace needs.
//
// The steps existed; the order did not. Following the obvious one — install the
// engine, then import — meant running `rig stack doctor --fix` first, because
// that is what installs josh. At that moment no member directory exists, so
// nothing crosses between members, so the overlay looks left over and doctor
// advised deleting it. Taking that advice throws away the file the workspace is
// about to need. The ordering was forced, so every first-timer met it.
//
// Doing the steps here, in an order where each can see what it is judging, puts
// doctor back to being a health check you run when something is wrong rather
// than a required setup step.
func newStackSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Set up a freshly cloned stackspace: engine, members, build overlay",
		Long: "Runs the steps a new clone needs, in an order where each can see what it\n" +
			"is judging: installs the fusion engine, reconstitutes the member\n" +
			"directories, writes the build overlay if it is missing, and prints the\n" +
			"status.\n\n" +
			"Safe to run again — every step is a no-op once it has been done.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			root, err := stackRoot(ctx)
			if err != nil {
				return err
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("no stack manifest here — `rig stack init` scaffolds one to fill in")
			}

			// 1. The engine, before anything that needs it. Acquiring it can
			//    mean a multi-minute build on a fresh machine, which is worth
			//    saying before the terminal goes quiet.
			fmt.Fprintln(out, "· fusion engine")
			if _, err := ensureJoshProxy(ctx, m.joshVersion(), out); err != nil {
				return err
			}

			// 2. The members. init is what knows how to reconstitute one at the
			//    commit its cursor names, and is already a no-op for a member
			//    that is present.
			fmt.Fprintln(out, "· members")
			if err := runStackSubcommand(cmd, "init"); err != nil {
				return err
			}

			// 3. The overlay, now that there is something to judge it against.
			fmt.Fprintln(out, "· build overlay")
			if err := runStackSubcommand(cmd, "wire"); err != nil {
				return err
			}

			// 4. What they have, in the words they will see from here on.
			fmt.Fprintln(out, "· status")
			return runStackSubcommand(cmd, "status")
		},
	}
}

// runStackSubcommand runs another `rig stack` verb in this process, so setup is
// the same code path as running the steps by hand rather than a second
// implementation of each that could drift from it.
func runStackSubcommand(parent *cobra.Command, name string) error {
	for _, c := range parent.Parent().Commands() {
		if c.Name() == name {
			c.SetOut(parent.OutOrStdout())
			c.SetErr(parent.ErrOrStderr())
			return c.RunE(c, nil)
		}
	}
	return fmt.Errorf("internal: `rig stack %s` is missing", name)
}
