package commands

import "github.com/spf13/cobra"

// NewRootCmd builds the codexrig command tree: the sync lifecycle, the account
// group, the scoped hook/config groups, and the interactive dashboard. main
// wires fang styling and the bare-TTY → dashboard routing around it; keeping the
// tree here lets consistency checks (core/cliguard) construct it without the
// runtime concerns.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "codexrig",
		Version: version,
		Short:   "Sync your Codex CLI configuration across machines, and run several logins side by side",
		Long: "codexRig syncs your Codex CLI config, instructions, skills and session\n" +
			"rollouts to your own git remote and restores them on any machine — rewriting\n" +
			"paths across OSes and never leaking secrets. It also keeps several Codex\n" +
			"logins on one machine, each in its own isolated CODEX_HOME.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		NewInitCmd(),
		NewSyncCmd(),
		NewPullCmd(),
		NewRestoreCmd(),
		NewStatusCmd(),
		NewSearchCmd(),
		NewRecentCmd(),
		NewGuardCmd(),
		NewGuideCmd(),
		NewDoctorCmd(version),
		NewConfigCmd(),
		NewMCPCmd(),
		NewAccountCmd(),
		NewUICmd(),
	)
	root.AddCommand(ScopeCommands()...)
	return root
}
