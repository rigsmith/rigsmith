package commands

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
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

			// Who this machine is logged in as. On a machine tracking several
			// accounts the live login CHANGES, and until something fails it is
			// invisible — which is exactly the state `status` should surface.
			printAccountLine(out, info.Account)

			if len(info.Hooks) > 0 {
				fmt.Fprintf(out, "  hooks     %v\n", info.Hooks)
			} else {
				fmt.Fprintf(out, "  hooks     %s\n", DimStyle.Render("not installed (run `clauderig hooks install`)"))
			}

			if len(info.Devices) > 0 {
				fmt.Fprintln(out, DimStyle.Render("  devices:"))
				for _, d := range info.Devices {
					self := ""
					if d.Name == info.Machine.Name {
						self = DimStyle.Render(" (this)")
					}
					fmt.Fprintf(out, "  %-12s %s  %s%s\n", d.Name, d.OS,
						DimStyle.Render(humanizeSince(d.LastSync)), self)
				}
			}
			return nil
		},
	}
}

// humanizeSince renders a coarse relative time for device last-sync display.
func humanizeSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// printAccountLine renders the machine-wide Claude Code login.
func printAccountLine(out io.Writer, a status.AccountInfo) {
	switch {
	case a.LoggedOut:
		fmt.Fprintf(out, "  account   %s\n", DimStyle.Render("not logged in"))
		return
	case a.Problem != "":
		fmt.Fprintf(out, "  account   %s\n", WarnStyle.Render(a.Problem))
		return
	case a.Email == "":
		fmt.Fprintf(out, "  account   %s\n", DimStyle.Render("not logged in"))
		return
	}
	line := a.Email
	if a.Subscription != "" {
		line += DimStyle.Render(" · " + a.Subscription)
	}
	// The alias is the handle the user chose and will type; the account id is
	// just the slugified email, so printing it would only lengthen the line.
	if a.Alias != "" {
		line += DimStyle.Render(" · " + a.Alias)
	}
	if a.Desynced {
		// The desync `account doctor` exists to catch: requests authenticate as
		// one account while Claude Code displays another.
		line += "  " + ErrStyle.Render("✗ desynced — run `clauderig account doctor`")
	}
	fmt.Fprintf(out, "  account   %s\n", line)
	if a.Untracked {
		fmt.Fprintf(out, "            %s\n", DimStyle.Render(
			"not tracked by clauderig — `clauderig account add` to capture it"))
	}
	if a.PointerEmail != "" {
		// clauderig's arrow points elsewhere, so `account list` is describing a
		// different account than the one this machine authenticates as.
		fmt.Fprintf(out, "            %s\n", WarnStyle.Render(
			"clauderig's active pointer says "+a.PointerEmail+" — `clauderig account doctor`"))
	}
}
