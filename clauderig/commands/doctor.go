package commands

import (
	"fmt"
	"os"

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
			osTok := currentOSToken()

			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}

			r := pathmap.NewResolver(pathmap.MapFolders{"HOME": home}, osTok, nil)

			fmt.Fprintln(out, HeaderStyle.Render("clauderig doctor"))
			fmt.Fprintf(out, "  os    %s\n", osTok)
			fmt.Fprintf(out, "  $HOME %s %s\n\n", home, OkStyle.Render("✓"))

			fmt.Fprintln(out, DimStyle.Render("  sample template resolution:"))
			for _, tmpl := range []string{"$HOME/Git/rigsmith", "~/.claude/plans"} {
				res := r.Resolve(tmpl)
				switch res.Status {
				case pathmap.StatusResolved:
					fmt.Fprintf(out, "  %s  →  %s\n", tmpl, res.Path)
				case pathmap.StatusUnconfigured:
					fmt.Fprintf(out, "  %s  %s unmapped (%s)\n", tmpl, WarnStyle.Render("⚠"), res.MissingToken)
				default:
					fmt.Fprintf(out, "  %s  %s %v\n", tmpl, ErrStyle.Render("✗"), res.Status)
				}
			}
			return nil
		},
	}
}
