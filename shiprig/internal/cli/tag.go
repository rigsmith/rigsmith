package cli

import (
	"fmt"

	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/core/gitutil"
	"github.com/spf13/cobra"
)

// newTagCmd creates git tags for each discovered package at its current version.
// Go modules use the module-path convention (`dir/vX.Y.Z` or `vX.Y.Z`); other
// ecosystems use `<name>@<version>` (the @changesets/net-changesets convention).
// Existing tags are skipped.
func newTagCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Create git tags for each package at its current version",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			created, skipped := 0, 0
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue // ignored packages are never tagged
				}
				tag := tagName(ecoOf[p.Name], p.Dir, p.Name, p.Version)
				if gitutil.TagExists(cmd.Context(), ws.Root, tag) {
					skipped++
					continue
				}
				if dryRun {
					fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("would tag"), tag)
					created++
					continue
				}
				if err := gitutil.CreateTag(cmd.Context(), ws.Root, tag, tag); err != nil {
					return fmt.Errorf("tagging %s: %w", p.Name, err)
				}
				fmt.Fprintf(out, "%s %s\n", commands.PatchStyle.Render("tagged"), tag)
				created++
			}
			fmt.Fprintf(out, "\n%d tag(s), %s\n", created, commands.DimStyle.Render(fmt.Sprintf("%d already present", skipped)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the tags without creating them")
	return cmd
}

// tagName builds the tag for a package. Go uses the module-path convention; all
// others use name@version. Delegates to gitutil.PackageTag so the tag/publish
// steps and the forge release step agree on the tag name.
func tagName(eco, dir, name, version string) string {
	return gitutil.PackageTag(eco, dir, name, version)
}
