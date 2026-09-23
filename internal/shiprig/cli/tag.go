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
	var (
		dryRun     bool
		outputPath string
	)
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Create git tags for each package at its current version",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// The event sink is opened first, as `changeset git-tag` does, so
			// it exists even when there is nothing to tag (see tagEvents.ready).
			// A dry run writes nothing, the file included.
			if events := openTagEvents(outputPath); events != nil && !dryRun {
				if err := events.ready(); err != nil {
					return err
				}
			}
			// A stackspace's history is several upstreams' fused together; a
			// tag on it names nothing any of them knows, and the history is
			// never pushed for one to be found. Nothing to do, and said so.
			if ws.Stackspace != nil {
				fmt.Fprintln(out, commands.DimStyle.Render("stackspace: a fused history is not tagged — nothing to do"))
				return nil
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			solo := singleApp(pkgs)
			// Tag events (see tagEvents): as `changeset git-tag`, a tag already
			// on the remote counts as existing too.
			events := openTagEvents(outputPath)
			eventRemote := ""
			if events != nil {
				eventRemote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			created, skipped := 0, 0
			// Distinct tags, not packages: a `tagTemplate` like "v${version}"
			// renders the same tag for every package, and one git ref should be
			// reported (and counted) once. Same reasoning as the publish tagging
			// phase.
			done := map[string]bool{}
			for _, p := range pkgs {
				if ws.Config.SkipsTag(p.Name) {
					continue // ignored packages, and private ones unless privatePackages.tag, are never tagged
				}
				tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, solo)
				if done[tag] {
					continue
				}
				done[tag] = true
				if gitutil.TagExists(cmd.Context(), ws.Root, tag) ||
					(eventRemote != "" && gitutil.RemoteTagExists(cmd.Context(), ws.Root, eventRemote, tag)) {
					skipped++
					continue
				}
				if dryRun {
					fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("would tag"), tag)
					created++
					continue
				}
				if events != nil {
					created, err := events.create(cmd.Context(), ws.Root, tag, p.Name)
					if err != nil {
						return fmt.Errorf("tagging %s: %w", p.Name, err)
					}
					if !created {
						skipped++
						continue
					}
				} else if err := gitutil.CreateTag(cmd.Context(), ws.Root, tag, tag); err != nil {
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
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "append a git-tag event per tag created to this file (default $CHANGESETS_OUTPUT)")
	return cmd
}
