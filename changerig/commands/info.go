package commands

import (
	"fmt"
	"sort"

	"github.com/rigsmith/core/changeset"
	"github.com/spf13/cobra"
)

// NewInfoCmd builds the `info` command.
func NewInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show resolved config and discovered packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			fmt.Fprintln(out, HeaderStyle.Render("Workspace"))
			fmt.Fprintf(out, "  root:        %s\n", ws.Root)
			fmt.Fprintf(out, "  initialized: %v\n", ws.Initialized())
			fmt.Fprintf(out, "  baseBranch:  %s\n", ws.Config.BaseBranch)
			fmt.Fprintf(out, "  access:      %s\n", ws.Config.Access)
			fmt.Fprintf(out, "  source:      %s\n\n", ws.Config.CommitSource())

			fmt.Fprintln(out, HeaderStyle.Render("Ecosystems"))
			detected, err := ws.Registry.DetectAll(cmd.Context(), ws.Root)
			if err != nil {
				return err
			}
			detectedSet := map[string]bool{}
			for _, id := range detected {
				detectedSet[id] = true
			}
			for _, eco := range ws.Registry.All() {
				mark := DimStyle.Render("—")
				if detectedSet[eco.Info().ID] {
					mark = PatchStyle.Render("✓")
				}
				fmt.Fprintf(out, "  %s %s (%s)\n", mark, eco.Info().DisplayName, eco.Info().ID)
			}

			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
			fmt.Fprintf(out, "\n%s\n", HeaderStyle.Render(fmt.Sprintf("Packages (%d)", len(pkgs))))
			for _, p := range pkgs {
				fmt.Fprintf(out, "  %s %s %s\n", p.Name, DimStyle.Render(p.Version), DimStyle.Render("["+ecoOf[p.Name]+"]"))
			}

			changesets, _ := changeset.Dir(ws.ChangesetDir, "")
			fmt.Fprintf(out, "\n%s %d\n", HeaderStyle.Render("Changesets:"), len(changesets))
			return nil
		},
	}
}
