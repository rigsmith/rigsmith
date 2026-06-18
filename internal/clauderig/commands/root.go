package commands

import "github.com/spf13/cobra"

// NewRootCmd builds the clauderig command tree: the sync lifecycle, the scoped
// hook/config groups, and the interactive dashboard. main wires fang styling and
// the bare-TTY → dashboard routing around it; keeping the tree here lets
// consistency checks (core/cliguard) construct it without the runtime concerns.
// version is the ldflags-stamped build version, threaded into the doctor command
// and cobra's --version.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "clauderig",
		Version: version,
		Short:   "Sync your Claude Code setup across machines, path-correct on restore",
		Long: "claudeRig syncs your Claude Code config, skills, and session history to your\n" +
			"own git remote and restores it on any machine — rewriting paths across OSes\n" +
			"and never leaking secrets. Pick up where you left off on a different computer.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		NewInitCmd(),
		NewSyncCmd(),
		NewPullCmd(),
		NewRestoreCmd(),
		NewStatusCmd(),
		NewGuardCmd(),
		NewGuideCmd(),
		NewDoctorCmd(version),
		NewConfigCmd(),
		NewMCPCmd(),
		NewAccountCmd(),
		NewUICmd(),
	)
	root.AddCommand(ScopeCommands()...) // global (alias: hooks) / project / local
	return root
}
