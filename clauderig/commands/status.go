package commands

import "github.com/spf13/cobra"

// NewStatusCmd builds the `status` command — remote reachability, local/behind
// state, last sync, per-root file counts, and the device registry. Plain styled
// output (scriptable); the live view lives in `ui`.
func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync state: remote, local changes, last sync, devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig status",
				"remote     reachability + ahead/behind",
				"roots      per-root file counts and dirty state",
				"devices    last-sync per machine (device registry)",
			)
			return nil
		},
	}
}
