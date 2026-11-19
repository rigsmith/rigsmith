package cli

import (
	"encoding/json"
	"fmt"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/forge"
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

// tagGroup is one git tag and the packages it covers. A git tag is one ref,
// but several packages can render the same one: a `tagTemplate` like
// "v${version}" puts every package at that version on a single tag.
type tagGroup struct {
	Tag      string
	Packages []plugin.Package
}

func (g tagGroup) names() []string {
	names := make([]string, len(g.Packages))
	for i, p := range g.Packages {
		names[i] = p.Name
	}
	return names
}

// tagGroups renders each taggable package's tag and groups the packages by
// it, in discovery order (a tag sits where its first package does). Ignored
// packages, and private ones unless privatePackages.tag, are never tagged.
func tagGroups(cfg *config.Config, pkgs []plugin.Package, ecoOf map[string]string, solo bool) []tagGroup {
	var groups []tagGroup
	at := map[string]int{}
	for _, p := range pkgs {
		if cfg.SkipsTag(p.Name) {
			continue
		}
		tag := gitutil.RenderTag(cfg.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, solo)
		if i, ok := at[tag]; ok {
			groups[i].Packages = append(groups[i].Packages, p)
			continue
		}
		at[tag] = len(groups)
		groups = append(groups, tagGroup{Tag: tag, Packages: []plugin.Package{p}})
	}
	return groups
}

// tagOutput is `shiprig tag --json`: every tag the run made (or, with
// --dry-run, would make) and every one already present, each with the
// packages it covers. A script reads which packages a tag releases from here
// rather than from the tag's name, which a `tagTemplate` makes arbitrary.
// Shaped like `forge-release --json`, with the same action words.
type tagOutput struct {
	DryRun bool      `json:"dryRun"`
	Tags   []tagJSON `json:"tags"`
	// Message says why nothing was tagged (a stackspace), or what failed.
	Message string `json:"message,omitempty"`
}

type tagJSON struct {
	Tag string `json:"tag"`
	// Action is forge.ActionCreated, ActionWouldCreate (--dry-run) or
	// ActionExists (present locally or, with tag events, on the remote).
	Action string `json:"action"`
	// Combined marks forgeRelease "combined"'s tag. It is usually a tag of
	// its own, which no package has (ReleaseOnly): its packages are the ones
	// the combined release covers, each also on a tag of its own, so a reader
	// working out which packages a run tags skips it. When the combined tag
	// is also packages' own tag (a lockstep "v${version}" for both), that
	// entry is marked Combined and lists those packages as any package tag
	// does.
	Combined    bool             `json:"combined,omitempty"`
	ReleaseOnly bool             `json:"releaseOnly,omitempty"`
	Packages    []tagPackageJSON `json:"packages"`
}

type tagPackageJSON struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Ecosystem string `json:"ecosystem,omitempty"`
}

func tagEntry(tag, action string, pkgs []plugin.Package, ecoOf map[string]string) tagJSON {
	entry := tagJSON{Tag: tag, Action: action, Packages: make([]tagPackageJSON, 0, len(pkgs))}
	for _, p := range pkgs {
		entry.Packages = append(entry.Packages, tagPackageJSON{Name: p.Name, Version: p.Version, Ecosystem: ecoOf[p.Name]})
	}
	return entry
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
		jsonOut    bool
		outputPath string
	)
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Create git tags for each package at its current version",
		Long: `Create a git tag for each package at its current version, skipping tags that
already exist. A tag several packages share (a tagTemplate like "v${version}")
is created once.

--json prints, on stdout, every tag made (or with --dry-run, that would be)
and every one already present, each with the packages it covers; the text
report goes to stderr instead. With --output or $CHANGESETS_OUTPUT, each tag
created appends one @changesets git-tag event per package it covers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// With --json, stdout carries only the JSON; the report goes to stderr.
			out := cmd.OutOrStdout()
			if jsonOut {
				out = cmd.ErrOrStderr()
			}
			result := tagOutput{DryRun: dryRun, Tags: []tagJSON{}}
			finish := func() error {
				if !jsonOut {
					return nil
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}
			// Every failure goes out through fail, so --json always prints
			// the envelope: the tags made before it, and the failure in its
			// message.
			fail := func(err error) error {
				cmd.SilenceUsage = true // not a usage error, and stdout is the JSON's
				result.Message = err.Error()
				if ferr := finish(); ferr != nil {
					return ferr
				}
				return err
			}
			ws, err := commands.Open()
			if err != nil {
				return fail(err)
			}
			// The event sink is opened first, as `changeset git-tag` does, so
			// it exists even when there is nothing to tag (see tagEvents.ready).
			// A dry run writes nothing, the file included.
			if events := openTagEvents(outputPath); events != nil && !dryRun {
				if err := events.ready(); err != nil {
					return fail(err)
				}
			}
			// A stackspace's history is several upstreams' fused together; a
			// tag on it names nothing any of them knows, and the history is
			// never pushed for one to be found. Nothing to do, and said so.
			if ws.Stackspace != nil {
				result.Message = "stackspace: a fused history is not tagged — nothing to do"
				fmt.Fprintln(out, commands.DimStyle.Render(result.Message))
				return finish()
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return fail(err)
			}
			groups := tagGroups(ws.Config, pkgs, ecoOf, singleApp(pkgs))
			// Tag events (see tagEvents): as `changeset git-tag`, a tag already
			// on the remote counts as existing too.
			events := openTagEvents(outputPath)
			eventRemote := ""
			if events != nil {
				eventRemote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			// forgeRelease "combined": settle the combined tag before any
			// package tag is made, so a refusal (packages on different
			// versions, or the tag already on another commit) leaves nothing
			// behind for a retry to trip over.
			if events == nil && notPerPackage(ws.Config) {
				var projected []plugin.Package
				var existing []tagGroup
				for _, g := range groups {
					if gitutil.TagExists(cmd.Context(), ws.Root, g.Tag) {
						existing = append(existing, g)
					} else {
						projected = append(projected, g.Packages...)
					}
				}
				// A refusal here comes before the tagging loop records
				// anything, so the envelope still lists what is known: the
				// tags already present, as a failure in the loop would.
				refuse := func(err error) error {
					for _, g := range existing {
						result.Tags = append(result.Tags, tagEntry(g.Tag, forge.ActionExists, g.Packages, ecoOf))
					}
					return fail(err)
				}
				atHead, err := releasedAtHead(cmd.Context(), ws, pkgs, ecoOf)
				if err != nil {
					return refuse(err)
				}
				ctag, err := combinedReleaseTag(ws.Config, unionByName(projected, atHead))
				if err != nil {
					return refuse(err)
				}
				if ctag != "" {
					if _, _, err := ensureCombinedTag(cmd.Context(), ws.Root, "", ctag, true); err != nil {
						return refuse(fmt.Errorf("tagging %s: %w", ctag, err))
					}
				}
			}
			// Distinct tags, not packages: one git ref is reported (and
			// counted) once, however many packages it covers. Same reasoning
			// as the publish tagging phase.
			created, skipped := 0, 0
			present := func(g tagGroup) {
				skipped++
				result.Tags = append(result.Tags, tagEntry(g.Tag, forge.ActionExists, g.Packages, ecoOf))
			}
			var taggedNow []plugin.Package // tagged (or would be) by this run
			for _, g := range groups {
				if gitutil.TagExists(cmd.Context(), ws.Root, g.Tag) ||
					(eventRemote != "" && gitutil.RemoteTagExists(cmd.Context(), ws.Root, eventRemote, g.Tag)) {
					present(g)
					continue
				}
				action := forge.ActionCreated
				if dryRun {
					fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("would tag"), g.Tag)
					action = forge.ActionWouldCreate
				} else {
					if events != nil {
						made, err := events.create(cmd.Context(), ws.Root, g.Tag, g.names())
						if err != nil {
							return fail(fmt.Errorf("tagging %s: %w", g.Packages[0].Name, err))
						}
						if !made {
							present(g)
							continue
						}
					} else if err := gitutil.CreateTag(cmd.Context(), ws.Root, g.Tag, g.Tag); err != nil {
						return fail(fmt.Errorf("tagging %s: %w", g.Packages[0].Name, err))
					}
					fmt.Fprintf(out, "%s %s\n", commands.PatchStyle.Render("tagged"), g.Tag)
				}
				created++
				taggedNow = append(taggedNow, g.Packages...)
				result.Tags = append(result.Tags, tagEntry(g.Tag, action, g.Packages, ecoOf))
			}
			// forgeRelease "combined": the one release's tag too, at the same
			// commit, so `git push --follow-tags` carries it with the rest. With
			// tag events the caller pushes only the tags reported, and a
			// combined tag belongs to no package, so `shiprig forge-release`
			// makes and pushes it instead.
			if events == nil && notPerPackage(ws.Config) {
				atHead, err := releasedAtHead(cmd.Context(), ws, pkgs, ecoOf)
				if err != nil {
					return fail(err)
				}
				covered := unionByName(taggedNow, atHead)
				ctag, err := combinedReleaseTag(ws.Config, covered)
				if err != nil {
					return fail(err)
				}
				// The combined tag is also packages' own tag (lockstep
				// "v${version}" for both): made and reported already, so it
				// is only marked, with nothing printed, counted or made again.
				if ctag != "" && !markCombined(result.Tags, ctag) {
					existed, line, err := ensureCombinedTag(cmd.Context(), ws.Root, "", ctag, dryRun)
					if err != nil {
						return fail(fmt.Errorf("tagging %s: %w", ctag, err))
					}
					action := forge.ActionExists
					if existed {
						skipped++
					} else {
						fmt.Fprintln(out, line)
						created++
						action = forge.ActionCreated
						if dryRun {
							action = forge.ActionWouldCreate
						}
					}
					entry := tagEntry(ctag, action, covered, ecoOf)
					entry.Combined, entry.ReleaseOnly = true, true
					result.Tags = append(result.Tags, entry)
				}
			}
			fmt.Fprintf(out, "\n%d tag(s), %s\n", created, commands.DimStyle.Render(fmt.Sprintf("%d already present", skipped)))
			return finish()
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the tags without creating them")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print each tag made (or that would be) or already present, with the packages it covers, as JSON on stdout (the report goes to stderr)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "append a git-tag event per package for each tag created to this file (default $CHANGESETS_OUTPUT)")
	return cmd
}

// markCombined marks the entry for tag as the combined release's, reporting
// whether there was one.
func markCombined(tags []tagJSON, tag string) bool {
	for i := range tags {
		if tags[i].Tag == tag {
			tags[i].Combined = true
			return true
		}
	}
	return false
}
