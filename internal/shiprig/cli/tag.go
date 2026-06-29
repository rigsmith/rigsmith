package cli

import (
	"fmt"

	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
)

// singleApp reports whether the repo is a single-app repo — exactly one
// discovered package — the signal for defaulting to the vX.Y.Z tag convention
// (see gitutil.RenderTag). It counts every discovered package, including any in
// the `ignore` list: a repo with a second (even ignored) package is a monorepo
// where `<name>@<version>` still earns its disambiguation, so the conservative
// default leaves those tags unchanged. Every tag site computes it from the same
// discovery so the created tag, the forge release, and the ${tag} variable agree.
func singleApp(pkgs []plugin.Package) bool {
	return len(pkgs) == 1
}

// newTagCmd creates git tags for each discovered package at its current version.
// Go modules use the module-path convention (`dir/vX.Y.Z` or `vX.Y.Z`); a
// single-app repo defaults to `vX.Y.Z`; other (multi-package) ecosystems use
// `<name>@<version>` (the @changesets/net-changesets convention). A config
// `tagTemplate` (e.g. "v${version}") overrides this for every package. Existing
// tags are skipped.
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
			solo := singleApp(pkgs)
			created, skipped := 0, 0
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue // ignored packages are never tagged
				}
				tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, solo)
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
