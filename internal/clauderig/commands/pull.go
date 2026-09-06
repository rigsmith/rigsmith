package commands

import (
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/spf13/cobra"
)

// NewPullCmd fetches into the staging repo and preserves configured fresh-machine
// auto-restore. As the SessionStart target, it never prompts and treats transport
// and restore failures as best-effort so they do not prevent a session starting.
func NewPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Fetch latest into the local staging repo (no write to ~/.claude)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			applicationService(out).Pull(ctx, service.PullRequest{Config: cfg, Machine: me, StagingDir: staging})
			return nil
		},
	}
}
