package commands

import "github.com/spf13/cobra"

// NewConfigCmd builds the `config` command — view/edit the clauderig config: the
// remote, machine identity + home maps (the single source of truth pathmap
// reads), roots, allowlist overrides, and retention. The `ui` editor pane writes
// the same file; the file always wins.
func NewConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "View or edit clauderig configuration (remote, machine maps, roots)",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig config",
				"machine maps   $HOME + per-OS/per-machine path overrides (pathmap source of truth)",
				"roots          which roots sync, allowlist overrides",
				"retention      history window + squash threshold",
			)
			return nil
		},
	}
}
