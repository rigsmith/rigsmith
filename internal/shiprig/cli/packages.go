package cli

import (
	"encoding/json"
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
// `ignore` list. The `list` subcommand prints and exits without the picker.
func newPackagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "packages",
		Short: "Show the packages a release will build; include/exclude them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := showPackages(cmd)
			if err != nil {
				return err
			}
			if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
				return RunPackagePicker(cmd.Context(), ws)
			}
			return nil
		},
	}
	cmd.AddCommand(newPackagesListCmd())
	return cmd
}

// newPackagesListCmd is the read-only companion: print the release packages and
// exit, never opening the picker (the `… list` convention shared with worktree /
// branch / mcp / account).
func newPackagesListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Print the release packages and exit (no interactive picker)",
		Long: `Print the release packages and exit (no interactive picker).

--json prints them for a script: every discovered package, in every
ecosystem, with its directory, current version, where its changelog goes, and
whether it is private, ignored, or releasing (and to what). Paths are relative
to the repository root, with forward slashes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !asJSON {
				_, err := showPackages(cmd)
				return err
			}
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			rps, err := commands.ReleasePackages(cmd.Context(), ws)
			if err != nil {
				return err
			}
			return writePackagesJSON(cmd.OutOrStdout(), rps)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the packages as JSON")
	return cmd
}

// packageJSON is one package in `packages list --json`: the wire format a
// script (shiprig-action) reads in place of an npm-only workspace lookup, so
// its field names are a contract.
type packageJSON struct {
	Name             string `json:"name"`
	Ecosystem        string `json:"ecosystem"`
	Dir              string `json:"dir"`
	Version          string `json:"version"`
	NextVersion      string `json:"nextVersion,omitempty"`
	Bump             string `json:"bump,omitempty"`
	Private          bool   `json:"private"`
	Ignored          bool   `json:"ignored"`
	Changelog        string `json:"changelog"`
	ChangelogSection string `json:"changelogSection,omitempty"`
}

func writePackagesJSON(out io.Writer, rps []commands.ReleasePkg) error {
	pkgs := make([]packageJSON, 0, len(rps))
	for _, p := range rps {
		pkgs = append(pkgs, packageJSON{
			Name: p.Name, Ecosystem: p.Eco, Dir: p.Dir, Version: p.Current,
			NextVersion: p.Next, Bump: p.Bump, Private: p.Private, Ignored: p.Ignored,
			Changelog: p.Changelog, ChangelogSection: p.ChangelogSection,
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Packages []packageJSON `json:"packages"`
	}{pkgs})
}

// showPackages discovers the release packages and prints the disposition table,
// returning the workspace so the caller can open the picker.
func showPackages(cmd *cobra.Command) (*commands.Workspace, error) {
	ws, err := commands.Open()
	if err != nil {
		return nil, err
	}
	rps, err := commands.ReleasePackages(cmd.Context(), ws)
	if err != nil {
		return nil, err
	}
	printPackages(cmd.OutOrStdout(), rps)
	return ws, nil
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
