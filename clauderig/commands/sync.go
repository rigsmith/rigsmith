package commands

import "github.com/spf13/cobra"

// NewSyncCmd builds the `sync` command — snapshot → redact secrets → secret-scan
// tripwire → rewrite slugs to portable form → commit (config on main, history on
// the orphan branch) → push. Streams the pipeline so redaction is visible, not
// magic. Safe to run from a hook (non-interactive, best-effort).
func NewSyncCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Snapshot, redact, rewrite, and push your Claude Code setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig sync",
				"snapshot       take a rollback-able pre-sync snapshot",
				"redact         strip inline secrets from settings.json (field-level)",
				"scan           entropy/regex tripwire — fail loudly if a token slips through",
				"rewrite        project slugs → portable $HOME form",
				"commit/push    config → main, history → orphan branch (size-based squash)",
			)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would sync without writing")
	return cmd
}
