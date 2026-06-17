package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newPackagesCmd builds `shiprig packages` — it shows the packages a release
// will build (release / private / ignored disposition) and, on a terminal, opens
// the include/exclude picker that persists choices to the changeset config
// `ignore` list. `--list` prints and exits without the picker.
func newPackagesCmd() *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "packages",
		Short: "Show the packages a release will build; include/exclude them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			rps, err := commands.ReleasePackages(cmd.Context(), ws)
			if err != nil {
				return err
			}
			printPackages(cmd.OutOrStdout(), rps)

			interactive := !list &&
				term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
			if interactive {
				return RunPackagePicker(cmd.Context(), ws)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "print the packages and exit (no interactive picker)")
	return cmd
}

// printPackages renders the release disposition table to stdout (kept clean so
// the picker, which draws on stderr, doesn't tangle with piped output).
func printPackages(out io.Writer, rps []commands.ReleasePkg) {
	if len(rps) == 0 {
		fmt.Fprintln(out, commands.DimStyle.Render("No packages discovered."))
		return
	}
	for _, p := range rps {
		fmt.Fprintf(out, "  %s %s  %s\n",
			commands.DimStyle.Render(fmt.Sprintf("%-9s", dispositionLabel(p))), p.Name, packageDetail(p))
	}
}

// dispositionLabel is the short status word for a package row.
func dispositionLabel(p commands.ReleasePkg) string {
	switch {
	case p.Ignored:
		return "ignored"
	case p.Releasing():
		return p.Bump
	default:
		return "—"
	}
}

func packageDetail(p commands.ReleasePkg) string {
	var detail string
	switch {
	case p.Ignored:
		detail = commands.DimStyle.Render("excluded from the release")
	case p.Releasing():
		detail = commands.DimStyle.Render(p.Current+" → ") + p.Next
	default:
		detail = commands.DimStyle.Render("no change (" + p.Current + ")")
	}
	if p.Private {
		detail += commands.DimStyle.Render("  · private (versioned, not published)")
	}
	return detail
}
