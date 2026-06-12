package commands

import (
	"fmt"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/core/pathmap"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds the `doctor` command — previews how path templates resolve
// on this machine and flags anything unmapped. It is the first command wired to
// the real engine (core/pathmap): the cross-OS rewrite that makes a synced
// session resumable on a different computer. Configurable machine maps land with
// `config`; for now it exercises the current machine's $HOME.
func NewDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Preview path resolution for this machine and flag unmapped paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cfg := config.Default()
			me := config.Detect("this")

			fmt.Fprintln(out, HeaderStyle.Render("clauderig doctor"))
			fmt.Fprintf(out, "  os    %s\n", me.OS)
			fmt.Fprintf(out, "  $HOME %s %s\n\n", me.Home, OkStyle.Render("✓"))

			fmt.Fprintln(out, DimStyle.Render("  sync roots (resolved for this machine):"))
			for _, r := range cfg.Roots {
				loc, st := cfg.RootLocation(r.ID, me)
				if st == pathmap.StatusResolved {
					fmt.Fprintf(out, "  %-8s →  %s\n", r.ID, loc)
				} else {
					fmt.Fprintf(out, "  %-8s %s %v\n", r.ID, WarnStyle.Render("⚠"), st)
				}
			}

			fmt.Fprintln(out, DimStyle.Render("\n  sample slug rewrite (this machine):"))
			res := me.Resolver().Resolve("$HOME/Git/rigsmith")
			fmt.Fprintf(out, "  $HOME/Git/rigsmith  →  %s\n", res.Path)
			return nil
		},
	}
}
