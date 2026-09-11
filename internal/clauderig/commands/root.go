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
		Short:   "Sync and restore Claude Code across machines, with an explicit foreground queue",
		Long: "claudeRig syncs your Claude Code config, skills, and session history to your\n" +
			"own git remote and restores it on any machine — rewriting paths across OSes\n" +
			"and never leaking secrets. Pick up where you left off on a different computer.\n\n" +
			"Use queue to save requests, inspect/retry work, sync with queued-request\n" +
			"acknowledgement, or run/drain a foreground worker (v2 preview).\n" +
			"Sync and hooks are synchronous by default; queue enable-hooks opts in locally.\nNo background service is installed.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		NewInitCmd(),
		NewSyncCmd(),
		NewQueueCmd(),
		NewPullCmd(),
		NewMergeCmd(),
		NewPeekCmd(),
		NewRestoreCmd(),
		NewStatusCmd(),
		NewRepoCmd(),
		NewSearchCmd(),
		NewRecentCmd(),
		NewLedgerCmd(),
		NewMoveCmd(),
		NewRerootCmd(),
		NewGuardCmd(),
		NewGuideCmd(),
		NewDoctorCmd(version),
		NewConfigCmd(),
		NewMCPCmd(),
		NewAccountCmd(),
		NewDesktopCmd(),
		NewDeviceCmd(),
		NewUICmd(),
	)
	root.AddCommand(ScopeCommands()...) // global (alias: hooks) / project / local
	return root
}
