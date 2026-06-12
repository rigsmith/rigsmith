package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/planner"
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
		verbose bool
		output  string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the pending release plan (what version would do)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			plan, err := BuildPlan(cmd.Context(), ws)
			if err != nil {
				return err
			}
			if output != "" {
				return writeStatusPlan(ws.Root, output, plan)
			}
			if len(plan) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("No changesets found — nothing to release."))
				return nil
			}
			PrintPlan(cmd.OutOrStdout(), plan, verbose)
			return nil
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show the changes driving each package")
	cmd.Flags().StringVar(&output, "output", "", "write the release plan as JSON to this file (matches @changesets status --output)")
	return cmd
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

// BuildPlan loads changesets + packages and runs the planner.
func BuildPlan(ctx context.Context, ws *Workspace) ([]*planner.Module, error) {
	changesets, err := changeset.Dir(ws.ChangesetDir, "")
	if err != nil {
		return nil, fmt.Errorf("reading changesets: %w", err)
	}
	pkgs, _, err := ws.Discover(ctx)
	if err != nil {
		return nil, err
	}
	return planner.Plan(changesets, pkgs, ws.Config), nil
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
