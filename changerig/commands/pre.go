package commands

import (
	"fmt"

	"github.com/rigsmith/core/prestate"
	"github.com/spf13/cobra"
)

// NewPreCmd builds the `pre` command: `pre enter <tag>` enters prerelease mode
// (writing .changeset/pre.json), `pre exit` marks it for graduation on the next
// `version` run.
func NewPreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "pre <enter|exit> [tag]",
		Short:     "Enter or exit prerelease mode",
		Args:      cobra.MinimumNArgs(1),
		ValidArgs: []string{"enter", "exit"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			switch args[0] {
			case "enter":
				if len(args) < 2 || args[1] == "" {
					return fmt.Errorf("pre enter requires a tag, e.g. `pre enter next`")
				}
				tag := args[1]
				if existing, _ := prestate.Read(ws.ChangesetDir); existing != nil && existing.Mode == prestate.ModePre {
					return fmt.Errorf("already in prerelease mode (tag %q)", existing.Tag)
				}
				pkgs, _, err := ws.Discover(cmd.Context())
				if err != nil {
					return err
				}
				initial := map[string]string{}
				for _, p := range pkgs {
					initial[p.Name] = p.Version
				}
				ps := &prestate.PreState{
					Mode:            prestate.ModePre,
					Tag:             tag,
					InitialVersions: initial,
					Changesets:      []string{},
				}
				if err := prestate.Write(ws.ChangesetDir, ps); err != nil {
					return err
				}
				fmt.Fprintf(out, "Entered prerelease mode with tag %q.\n", tag)
				fmt.Fprintln(out, DimStyle.Render("Run `version` to produce "+tag+" prereleases; `pre exit` then `version` to graduate."))
				return nil

			case "exit":
				ps, err := prestate.Read(ws.ChangesetDir)
				if err != nil {
					return err
				}
				if ps == nil {
					return fmt.Errorf("not in prerelease mode")
				}
				ps.Mode = prestate.ModeExit
				if err := prestate.Write(ws.ChangesetDir, ps); err != nil {
					return err
				}
				fmt.Fprintln(out, "Exiting prerelease mode.")
				fmt.Fprintln(out, DimStyle.Render("Run `version` to graduate prerelease packages to their stable versions."))
				return nil

			default:
				return fmt.Errorf("unknown pre action %q (want enter|exit)", args[0])
			}
		},
	}
	return cmd
}
