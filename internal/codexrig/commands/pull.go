package commands

import (
	"fmt"

	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/service"
	"github.com/spf13/cobra"
)

// NewPullCmd builds `codexrig pull`, the session-start half of the loop.
func NewPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Bring down what another machine synced",
		Long: "Fetches the sync repo and settles anything another machine pushed.\n\n" +
			"With autoRestore on it will also restore onto a machine that has no Codex\n" +
			"setup yet — and only such a machine. Nothing here is fatal: this runs from a\n" +
			"session-start hook, and a session has to begin whether or not the network is\n" +
			"up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// Reported like every other failure here, not returned. The two
			// lines below used to break the contract this command states two
			// paragraphs down: a config.json that does not parse, or a home
			// that cannot be resolved, would exit non-zero from a session-start
			// hook — which is the one thing this command must never do.
			cfg, err := config.LoadOrDefault()
			if err != nil {
				fmt.Fprintf(out, "  %s %v\n", WarnStyle.Render("!"), err)
				return nil
			}
			staging, err := config.StagingDir()
			if err != nil {
				fmt.Fprintf(out, "  %s %v\n", WarnStyle.Render("!"), err)
				return nil
			}
			svc := service.Service{Observe: renderEvent(out)}
			res := svc.Pull(cmd.Context(), service.PullRequest{
				Config: cfg, Machine: config.DetectFor(cfg), StagingDir: staging,
			})
			// Reported, never returned: exiting non-zero from a session-start
			// hook is how a backup tool stops somebody working.
			for _, e := range []error{res.CloneErr, res.FetchErr, res.RestoreErr} {
				if e != nil {
					fmt.Fprintf(out, "  %s %v\n", WarnStyle.Render("!"), e)
				}
			}
			return nil
		},
	}
}
