package commands

import "github.com/spf13/cobra"

// NewRootCmd builds the brewrig command tree. main wires fang styling and the
// bare-TTY → dashboard routing around it; keeping the tree here lets the
// consistency checks (core/cliguard) construct it without the runtime concerns.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "brewrig",
		Version: version,
		Short:   "Keep several Macs on the same Homebrew software, and the same versions of it",
		Long: "brewRig publishes what each machine has deliberately installed to your own\n" +
			"private git repo, and installs whatever a machine is missing. It never\n" +
			"uninstalls to make machines match — the one exception is a package you\n" +
			"deliberately removed elsewhere, which it offers one at a time.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		NewInitCmd(),
		NewSyncCmd(),
		NewApplyCmd(),
		NewStatusCmd(),
		NewUpdateCmd(),
		NewSkipCmd(),
		NewDoctorCmd(version),
		NewUICmd(),
	)
	return root
}
