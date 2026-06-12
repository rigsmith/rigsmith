package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/status"
	"github.com/spf13/cobra"
)

// NewStatusCmd builds the `status` command — a read-only summary of sync state:
// machine, remote reachability, last sync, per-root file counts, and hooks. Plain
// styled output (scriptable); the live view lives in `ui`.
func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync state: remote, last sync, roots, hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, _ := config.StagingDir()
			settings, _ := settingsPath()
			info := status.Gather(ctx, cfg, me, staging, settings)

			fmt.Fprintln(out, HeaderStyle.Render("clauderig status"))
			fmt.Fprintf(out, "  machine   %s (%s)\n", info.Machine.Name, info.Machine.OS)

			if info.Remote == "" {
				fmt.Fprintf(out, "  remote    %s\n", DimStyle.Render("none configured — run `clauderig init`"))
			} else {
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				reach := gitrepo.Reachable(rctx, info.Remote)
				cancel()
				mark := ErrStyle.Render("unreachable")
				if reach {
					mark = OkStyle.Render("reachable")
				}
				fmt.Fprintf(out, "  remote    %s  %s\n", info.Remote, mark)
			}

			if info.LastSync != "" {
				fmt.Fprintf(out, "  last sync %s\n", info.LastSync)
			} else {
				fmt.Fprintf(out, "  last sync %s\n", DimStyle.Render("never (no staging repo yet)"))
			}
			if info.Dirty {
				fmt.Fprintf(out, "            %s\n", WarnStyle.Render("staging has uncommitted changes"))
			}

			fmt.Fprintln(out, DimStyle.Render("  roots:"))
			for _, r := range info.Roots {
				if !r.Present {
					fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("absent here"))
					continue
				}
				fmt.Fprintf(out, "  %-8s %d files\n", r.ID, r.Files)
			}

			if len(info.Hooks) > 0 {
				fmt.Fprintf(out, "  hooks     %v\n", info.Hooks)
			} else {
				fmt.Fprintf(out, "  hooks     %s\n", DimStyle.Render("not installed (run `clauderig hooks install`)"))
			}
			return nil
		},
	}
}
