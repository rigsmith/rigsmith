package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/prestate"
	"github.com/rigsmith/rigsmith/core/since"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// statusRelease is one entry in the machine-readable release plan. The shape
// ({ name, type, newVersion }) matches @changesets' `status --output` and
// net-changesets, so the plan is a cross-implementation oracle for version
// decisions independent of changelog formatting.
type statusRelease struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NewVersion string `json:"newVersion"`
}

type statusPlan struct {
	Releases []statusRelease `json:"releases"`
}

// NewStatusCmd builds the `status` command.
func NewStatusCmd() *cobra.Command {
	var (
		verbose  bool
		output   string
		sinceRef string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the pending release plan (what version would do)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			// An uninitialized workspace (e.g. bare `shiprig`, which lands here)
			// shouldn't hard-error — offer source-aware setup, then point at the
			// next step. A freshly scaffolded workspace has nothing to release
			// yet, so don't fall through to the no-changesets gate below.
			if !ws.Initialized() {
				ready, err := offerSetup(cmd, ws)
				if err != nil {
					return err
				}
				if !ready {
					return nil
				}
				printSetupNextStep(cmd, ws)
				return nil
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			// --since narrows the plan to what the branch adds since the ref:
			// its changesets (as @changesets and net-changesets do) and, with
			// commits as a source, its commits. An explicit ref is validated
			// whatever the source, so a mistyped one never passes silently.
			var changedFiles []string
			changesetMode := ws.Config.CommitSource() == config.SourceChangesets
			if sinceRef != "" {
				if changedFiles, err = ws.NarrowSince(cmd.Context(), sinceRef); err != nil {
					return err
				}
			}
			changesets, fromCommits, err := ws.LoadChangesets(cmd.Context(), pkgs)
			if err != nil {
				return err
			}

			// The run after `pre exit` graduates the changesets waiting in
			// .changeset/pre/, even when none are left at the top level; count
			// them before deciding there is nothing to report.
			pre, err := prestate.Read(ws.ChangesetDir)
			if err != nil {
				return err
			}
			if changesets, err = withGraduating(ws, changesets, pre); err != nil {
				return err
			}

			// The CI gate, as @changesets v3 has it: fail when a package that
			// would version (not ignored, and not private unless
			// privatePackages.version) changed since the ref — --since, else
			// the base branch — and there is no changeset at all. Commit mode
			// has no changeset to require. Without --since, a base that can't
			// be compared against (no git, no such branch) gates nothing.
			if changesetMode && len(changesets) == 0 {
				ref := sinceRef
				if ref == "" {
					ref = ws.Config.BaseBranch
					if ref == "" {
						ref = "main"
					}
					changedFiles, _ = gitutil.ChangedFilesSince(cmd.Context(), ws.Root, ref)
				}
				var changed []string
				for _, name := range since.ChangedProjectNames(changedFiles, pkgs, ws.Root) {
					if !ws.Config.IsIgnored(name) {
						changed = append(changed, name)
					}
				}
				if len(changed) > 0 {
					return fmt.Errorf("some projects have changed since %q but no changeset was found (%s) — run `changerig add` to add one, or `changerig add --empty` if no release is needed",
						ref, strings.Join(changed, ", "))
				}
			}

			// Nothing pending is not a failure (@changesets v3 prints an empty
			// list and exits 0); --output still writes the (empty) plan, which
			// is how a script tells "nothing to release" from an error.
			if len(changesets) == 0 {
				if output != "" {
					return writeStatusPlan(ws.Root, output, nil)
				}
				if fromCommits || ws.Config.UsesCommits() {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("No releasable commits since the last release."))
					return nil
				}
				// On a real terminal, show the source, the packages at their
				// current versions, and the next step.
				if sinceRef == "" && term.IsTerminal(os.Stdout.Fd()) {
					printEmptyStatusPanel(cmd, ws, pkgs, ecoOf)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("No changesets — nothing to release."))
				return nil
			}

			plan, err := assemblePlan(cmd.Context(), ws, changesets, pkgs)
			if err != nil {
				return err
			}
			if output != "" {
				return writeStatusPlan(ws.Root, output, plan)
			}
			if len(plan) == 0 {
				out := cmd.OutOrStdout()
				fmt.Fprintln(out, DimStyle.Render("Changesets found, but nothing to release."))
				// Say which files and why. "Nothing to release" while holding
				// changesets is a contradiction the tool can resolve itself — it
				// knows each file names no package, an unknown one, or only
				// ignored ones. Leaving that unsaid let sixteen inert changesets
				// accumulate here across a release.
				if stranded := FindStranded(changesets, pkgs, ws.Config); len(stranded) > 0 {
					fmt.Fprintln(out)
					width := 0
					for _, s := range stranded {
						width = max(width, len(s.ID))
					}
					for _, s := range stranded {
						fmt.Fprintf(out, "  %-*s  %s\n", width, s.ID, DimStyle.Render(s.Reason))
					}
					fmt.Fprintln(out)
					fmt.Fprintln(out, DimStyle.Render(StrandedHint(cmd.Root().Name(), pkgs, ws.Config)))
				}
				return nil
			}
			PrintPlan(cmd.OutOrStdout(), plan, verbose)
			// A package with no version in the tree and none recorded plans
			// from 0.0.0, and the plan line cannot say that on its own.
			printUnversionedNote(cmd.OutOrStdout(), pkgs)
			// After the plan: the nudge is about how the changelogs will READ,
			// which only makes sense once you can see what is about to release.
			// The same set the plan was built from: a changeset a prerelease has
			// already consumed renders nothing this run, so warning about it
			// would be advice about a changelog nobody is about to write.
			pre, perr := prestate.Read(ws.ChangesetDir)
			if perr != nil {
				return perr
			}
			if found := FindUnmentioned(activeChangesets(changesets, pre), ws.Config); len(found) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				PrintUnmentioned(cmd.OutOrStdout(), found, cmd.Root().Name())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show the changes driving each package")
	cmd.Flags().StringVar(&output, "output", "", "write the release plan as JSON to this file (matches @changesets status --output)")
	cmd.Flags().StringVar(&sinceRef, "since", "", "only consider changes since this git ref; fail if projects changed without a changeset")
	return cmd
}

// printSetupNextStep tells a just-onboarded user how to produce a release, so a
// bare command that triggered setup ends on source-appropriate guidance rather
// than the no-changesets error a freshly scaffolded workspace would otherwise
// hit.
func printSetupNextStep(cmd *cobra.Command, ws *Workspace) {
	out := cmd.OutOrStdout()
	tool := cmd.Root().Name()
	switch ws.Config.CommitSource() {
	case config.SourceCommits:
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("Write a conventional commit (e.g. `feat(pkg): …`), then re-run `%s` to see the plan.", tool)))
	case config.SourceBoth:
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("Write a conventional commit or run `%s add`, then re-run `%s` to see the plan.", tool, tool)))
	default:
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("Add a changeset with `%s add`, then re-run `%s` to see the plan.", tool, tool)))
	}
}

// printEmptyStatusPanel renders the friendly "nothing pending" overview shown on
// an interactive terminal in changeset mode: the active source, the discovered
// packages at their current versions, and the next step. It replaces the hard
// "no changesets found" gate error, which is preserved for scripted/CI use.
func printEmptyStatusPanel(cmd *cobra.Command, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s\n", HeaderStyle.Render("Source:"), ws.Config.CommitSource())

	sorted := append([]plugin.Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	fmt.Fprintf(out, "\n%s\n", HeaderStyle.Render(fmt.Sprintf("Packages (%d)", len(sorted))))
	for _, p := range sorted {
		fmt.Fprintf(out, "  %s %s %s\n", p.Name, DimStyle.Render(versionLabel(p.Version)), DimStyle.Render("["+ecoOf[p.Name]+"]"))
	}
	printUnversionedNote(out, sorted)

	fmt.Fprintf(out, "\n%s\n", DimStyle.Render("Nothing to release yet."))
	printSetupNextStep(cmd, ws)
}

// writeStatusPlan serializes the plan as { releases: [{ name, type, newVersion }] }.
// A relative path is resolved against the workspace root, matching @changesets.
func writeStatusPlan(root, output string, plan []*planner.Module) error {
	releases := make([]statusRelease, 0, len(plan))
	for _, m := range plan {
		releases = append(releases, statusRelease{
			Name:       m.Name,
			Type:       m.HighestBump().String(),
			NewVersion: m.ResolvedVersion(),
		})
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Name < releases[j].Name })

	data, err := json.MarshalIndent(statusPlan{Releases: releases}, "", "  ")
	if err != nil {
		return err
	}
	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, output)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// BuildPlan loads changesets + packages and assembles the release plan,
// reflecting prerelease mode the same way `version` does.
func BuildPlan(ctx context.Context, ws *Workspace) ([]*planner.Module, error) {
	pkgs, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return nil, err
	}
	changesets, _, err := ws.LoadChangesets(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	// Reflect per-ecosystem versionStrategy overrides so status shows the same
	// targets `version` would write.
	ws.Config.PerPackageStrategy = ws.Config.StrategyByPackage(ecoOf)
	return assemblePlan(ctx, ws, changesets, pkgs)
}

// assemblePlan runs the planner over the changesets, reflecting prerelease
// mode (pre.json) the same way the version command does — status must never
// show a different target version than the release that would follow.
// Snapshot has no status equivalent.
// activeChangesets drops the changesets a prerelease run has already consumed.
// They stay on disk through a prerelease, so anything reasoning about what THIS
// release will render has to filter them out — the plan does, and so must
// anything reported alongside it.
func activeChangesets(changesets []*changeset.Changeset, pre *prestate.PreState) []*changeset.Changeset {
	if pre == nil || pre.Mode != prestate.ModePre {
		return changesets
	}
	var active []*changeset.Changeset
	for _, cs := range changesets {
		if !pre.Contains(cs.ID) {
			active = append(active, cs)
		}
	}
	return active
}

// withGraduating adds, on the run that exits prerelease mode, the changesets a
// prerelease already consumed into .changeset/pre/: the stable release
// consolidates every change since pre mode was entered (@changesets v3). Any
// other run gets changesets back unchanged.
func withGraduating(ws *Workspace, changesets []*changeset.Changeset, pre *prestate.PreState) ([]*changeset.Changeset, error) {
	if pre == nil || pre.Mode != prestate.ModeExit || !ws.Config.UsesChangesets() || !ws.Graduates() {
		return changesets, nil
	}
	graduating, err := changeset.Dir(prestate.Dir(ws.ChangesetDir), "")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return changesets, nil
		}
		return nil, fmt.Errorf("reading prerelease changesets: %w", err)
	}
	seen := make(map[string]bool, len(changesets))
	for _, cs := range changesets {
		seen[cs.ID] = true
	}
	out := append([]*changeset.Changeset{}, changesets...)
	for _, cs := range graduating {
		if !seen[cs.ID] {
			out = append(out, cs)
		}
	}
	return out, nil
}

func assemblePlan(ctx context.Context, ws *Workspace, changesets []*changeset.Changeset, pkgs []plugin.Package) ([]*planner.Module, error) {
	pre, err := prestate.Read(ws.ChangesetDir)
	if err != nil {
		return nil, err
	}
	changesets, err = withGraduating(ws, changesets, pre)
	if err != nil {
		return nil, err
	}

	active := activeChangesets(changesets, pre)

	plan := planner.Plan(active, pkgs, ws.Config)
	switch {
	case pre != nil && pre.Mode == prestate.ModePre:
		planner.ApplyPre(plan, pre.Tag)
	case pre != nil && pre.Mode == prestate.ModeExit && ws.Graduates():
		plan = planner.GraduatePrereleases(plan, pkgs)
	}
	return plan, nil
}

// PrintPlan renders a release plan to w.
func PrintPlan(w io.Writer, plan []*planner.Module, verbose bool) {
	sort.Slice(plan, func(i, j int) bool {
		if plan[i].HighestBump() != plan[j].HighestBump() {
			return plan[i].HighestBump() > plan[j].HighestBump()
		}
		return plan[i].Name < plan[j].Name
	})
	for _, m := range plan {
		label := styleFor(m.HighestBump()).Render(fmt.Sprintf("%-6s", m.HighestBump().String()))
		fmt.Fprintf(w, "  %s %s  %s → %s\n", label, m.DisplayName, DimStyle.Render(m.Current.String()), m.ResolvedVersion())
		if verbose {
			for _, c := range m.Changes {
				fmt.Fprintf(w, "         %s %s\n", DimStyle.Render("•"), firstLine(c.Description))
			}
		}
	}
}
