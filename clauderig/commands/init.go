package commands

import "github.com/spf13/cobra"

// NewInitCmd builds the `init` command — the first-run wizard (huh, ~5 steps):
// remote (create via gh / paste URL) → machine identity + home maps → roots &
// retention → secrets confirm → install hooks? → first sync. Idempotent.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "First-run wizard: configure remote, machine identity, roots, and hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig init",
				"1. remote        create a private repo via gh, or paste a git URL",
				"2. identity      this machine's name + $HOME, plus other machines' homes",
				"3. roots         ~/.claude (CLI) + app-support Claude (Desktop), retention",
				"4. secrets       confirm what gets stripped (never synced)",
				"5. hooks         install SessionStart→pull / Stop→push (optional)",
			)
			return nil
		},
	}
}
