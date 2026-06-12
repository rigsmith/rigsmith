package commands

import "github.com/spf13/cobra"

// NewRestoreCmd builds the `restore` command — pull, then write the allowlist to
// this machine with paths rewritten for this OS. Shows the rewrite + write/skip
// preview before touching the tree; on a non-empty ~/.claude the user chooses
// back-up-then-proceed or abort (default abort under no-TTY). --backup / --force.
func NewRestoreCmd() *cobra.Command {
	var backup, force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore your Claude Code setup here, rewriting paths for this OS",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig restore",
				"preview        show path rewrites ($HOME/Git/x → this machine) + write/skip set",
				"safety         non-empty ~/.claude → back up & proceed, or abort (default abort, no-TTY)",
				"write          materialise config + 30d history; re-slug projects for this OS",
				"flags          --backup (auto-backup), --force (skip prompt)",
			)
			return nil
		},
	}
	cmd.Flags().BoolVar(&backup, "backup", false, "back up existing ~/.claude before restoring")
	cmd.Flags().BoolVar(&force, "force", false, "restore without prompting (overwrites)")
	return cmd
}
