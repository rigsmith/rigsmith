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
			changesets, fromCommits, err := ws.LoadChangesets(cmd.Context(), pkgs)
			if err != nil {
				return err
			}

			// --since: guard against changes with no changeset, then narrow the
			// displayed changesets to those added since the ref (mirrors
			// @changesets and net-changesets). The gate is changeset-file
			// specific; in commit mode the commits themselves are the source, so
			// there is nothing to require.
			if sinceRef != "" && ws.Config.CommitSource() == config.SourceChangesets {
				changedFiles, err := gitutil.ChangedFilesSince(cmd.Context(), ws.Root, sinceRef)
				if err != nil {
					return fmt.Errorf("could not determine changes since %q: %w", sinceRef, err)
				}
				changedProjects := since.ChangedProjectNames(changedFiles, pkgs, ws.Root)
				if len(changedProjects) > 0 && !since.AnyChangesetAdded(changedFiles, ws.ChangesetDir) {
					return fmt.Errorf("some projects have changed since %q but no changeset was found (%s) — run `changerig add` to add one, or `changerig add --empty` if no release is needed",
						sinceRef, strings.Join(changedProjects, ", "))
				}
				ids := since.ChangedChangesetIDs(changedFiles, ws.ChangesetDir)
				inSince := map[string]bool{}
				for _, id := range ids {
					inSince[id] = true
				}
				kept := changesets[:0]
				for _, cs := range changesets {
					if inSince[cs.ID] {
						kept = append(kept, cs)
					}
				}
				changesets = kept
			}

			// A missing changeset is a failure in changeset mode, like @changesets
			// and net-changesets (the CI gate this command exists for). In commit
			// mode there is no changeset to require — no qualifying commits since
			// the last release simply means "nothing to release".
			if len(changesets) == 0 {
				if fromCommits || ws.Config.UsesCommits() {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("No releasable commits since the last release."))
					return nil
				}
				// On a real terminal (not a pipe/CI, and not the machine-readable
				// --output or the --since gate), a bare `shiprig`/`status` with
				// nothing pending shouldn't dead-end on a red error — show the
				// source, the packages at their current versions, and the next
				// step. The hard error stays for scripted/CI use, which is the
				// whole reason `status` exists.
				if output == "" && sinceRef == "" && term.IsTerminal(os.Stdout.Fd()) {
					printEmptyStatusPanel(cmd, ws, pkgs, ecoOf)
					return nil
				}
				return errors.New("no changesets found")
			}

			plan, err := assemblePlan(cmd.Context(), ws, changesets, pkgs)
			if err != nil {
				return err
			}
			if output != "" {
				return writeStatusPlan(ws.Root, output, plan)
			}
			if len(plan) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("Changesets found, but nothing to release."))
				return nil
			}
			PrintPlan(cmd.OutOrStdout(), plan, verbose)
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
		fmt.Fprintf(out, "  %s %s %s\n", p.Name, DimStyle.Render(p.Version), DimStyle.Render("["+ecoOf[p.Name]+"]"))
	}

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
func assemblePlan(ctx context.Context, ws *Workspace, changesets []*changeset.Changeset, pkgs []plugin.Package) ([]*planner.Module, error) {
	pre, err := prestate.Read(ws.ChangesetDir)
	if err != nil {
		return nil, err
	}

	active := changesets
	if pre != nil && pre.Mode == prestate.ModePre {
		active = nil
		for _, cs := range changesets {
			if !pre.Contains(cs.ID) {
				active = append(active, cs)
			}
		}
	}

	plan := planner.Plan(active, pkgs, ws.Config)
	switch {
	case pre != nil && pre.Mode == prestate.ModePre:
		planner.ApplyPre(plan, pre.Tag)
	case pre != nil && pre.Mode == prestate.ModeExit:
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
