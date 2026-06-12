package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/core/changelog"
	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/mdfmt"
	"github.com/rigsmith/core/planner"
	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/prestate"
	"github.com/spf13/cobra"
)

// NewVersionCmd builds the `version` command, including snapshot (--snapshot) and
// prerelease (driven by .changeset/pre.json) modes.
func NewVersionCmd() *cobra.Command {
	var (
		dryRun           bool
		snapshotTag      string
		snapshotTemplate string
	)
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Consume changesets: bump versions and write changelogs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Cobra's NoOptDefVal only binds `--snapshot=tag`; accept the
			// @changesets spelling `--snapshot tag` too (the tag lands in args).
			if cmd.Flags().Changed("snapshot") && strings.TrimSpace(snapshotTag) == "" && len(args) > 0 {
				snapshotTag = args[0]
			}
			ws, err := Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			changesets, err := changeset.Dir(ws.ChangesetDir, "")
			if err != nil {
				return err
			}
			pre, err := prestate.Read(ws.ChangesetDir)
			if err != nil {
				return err
			}

			// Determine the release mode.
			mode := planner.ModeNormal
			switch {
			case cmd.Flags().Changed("snapshot"):
				mode = planner.ModeSnapshot
			case pre != nil && pre.Mode == prestate.ModePre:
				mode = planner.ModePre
			case pre != nil && pre.Mode == prestate.ModeExit:
				mode = planner.ModeExit
			}

			// In prerelease mode only the not-yet-consumed changesets drive the run
			// (their summaries shouldn't re-appear each version); the prerelease
			// counter still advances from the current version.
			active := changesets
			if mode == planner.ModePre {
				active = nil
				for _, cs := range changesets {
					if !pre.Contains(cs.ID) {
						active = append(active, cs)
					}
				}
			}

			if len(active) == 0 && mode != planner.ModeExit {
				fmt.Fprintln(out, DimStyle.Render("No changesets — nothing to version."))
				return nil
			}

			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}

			// changelog-git / changelog-github enrichment: resolve the commit
			// (and PR/author) that added each changeset and decorate the summary's
			// first line before planning. Every lookup failure degrades to an
			// undecorated line — enrichment never fails the run.
			setting := changelog.ParseSetting(ws.Config)
			if setting.Kind != changelog.KindDefault {
				ids := make([]string, 0, len(active))
				for _, cs := range active {
					ids = append(ids, cs.ID)
				}
				infos := changelog.Resolve(ids, setting, ws.Root, execRunner(cmd))
				for _, cs := range active {
					if info, ok := infos[cs.ID]; ok {
						cs.Summary = changelog.RenderLine(cs.Summary, setting, &info)
					}
				}
			}

			plan := planner.Plan(active, pkgs, ws.Config)

			// Apply the mode's version overrides.
			switch mode {
			case planner.ModeSnapshot:
				template := snapshotTemplate
				if template == "" {
					template = ws.Config.Snapshot.PrereleaseTemplate
				}
				suffix, err := planner.SnapshotSuffix(template, strings.TrimSpace(snapshotTag), gitutil.ShortHead(cmd.Context(), ws.Root), time.Now())
				if err != nil {
					return err
				}
				planner.ApplySnapshot(plan, ws.Config.Snapshot.UseCalculatedVersion, suffix)
			case planner.ModePre:
				planner.ApplyPre(plan, pre.Tag)
			case planner.ModeExit:
				plan = planner.GraduatePrereleases(plan, pkgs)
			}

			if len(plan) == 0 {
				fmt.Fprintln(out, DimStyle.Render("Nothing to version."))
				return nil
			}

			PrintPlan(out, plan, false)
			if dryRun {
				fmt.Fprintln(out, DimStyle.Render("\n(dry run — no files written)"))
				return nil
			}

			// The three @changesets generators (default, changelog-git,
			// changelog-github) all render the default layout — git/github only
			// decorate the release lines (done above). Anything else resolves as
			// an external plugin.
			genSpec := ws.Config.ChangelogSpec()
			if setting.Kind != changelog.KindDefault {
				genSpec = "default"
			}
			gen, _ := plugin.ResolveChangelogGenerator(genSpec, ws.Root, planner.Builtins(ws.Config.Groups()))

			var changelogPaths []string
			for _, m := range plan {
				if eco, ok := ws.EcosystemFor(ecoOf[m.Name]); ok {
					req := plugin.SetVersionRequest{
						RepoRoot:          ws.Root,
						Package:           plugin.Package{Name: m.Name, ManifestPath: m.ManifestPath, VersionFile: m.VersionFile},
						NewVersion:        m.ResolvedVersion(),
						DependencyUpdates: m.DepUpdates,
					}
					if err := eco.SetVersion(cmd.Context(), req); err != nil {
						return fmt.Errorf("set version for %s: %w", m.Name, err)
					}
				}
				if m.RangeOnly {
					continue // "none" release: ranges rewritten, no version bump, no changelog
				}
				entry, err := gen.Render(cmd.Context(), planner.ModuleToRequest(m))
				if err != nil {
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				pkgDir := filepath.Dir(filepath.Join(ws.Root, m.ManifestPath))
				if err := changelog.WriteEntry(pkgDir, m.DisplayName, entry); err != nil {
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				changelogPaths = append(changelogPaths, filepath.Join(pkgDir, changelog.FileName))
			}

			// Formatting pass over the touched changelogs, per the `format`
			// config (false/absent = off; "native" runs in-process; "auto"
			// detects a tool; an argv array runs a custom command as written;
			// failures only warn).
			warnf := func(format string, a ...any) {
				fmt.Fprintln(out, DimStyle.Render("warn "+fmt.Sprintf(format, a...)))
			}
			if argv, ok := ws.Config.FormatCommand(); ok {
				mdfmt.FormatFilesCustom(changelogPaths, argv, ws.Root, mdfmt.Runner(execRunner(cmd)), warnf)
			} else {
				mdfmt.FormatFiles(changelogPaths, ws.Config.FormatSpec(), ws.Root, mdfmt.Runner(execRunner(cmd)), warnf)
			}

			// Changeset disposal + pre-state bookkeeping per mode.
			switch mode {
			case planner.ModeSnapshot:
				// Snapshot consumes changesets like a normal run (verified against
				// @changesets v3.0.0-next.5); the run is throwaway because the
				// working-tree changes are never committed.
				for _, cs := range changesets {
					_ = os.Remove(filepath.Join(ws.ChangesetDir, cs.ID+".md"))
				}
				fmt.Fprintf(out, "\nSnapshot-versioned %d package(s); removed %d changeset(s).\n", len(plan), len(changesets))
			case planner.ModePre:
				for _, cs := range active {
					pre.Changesets = append(pre.Changesets, cs.ID)
				}
				if err := prestate.Write(ws.ChangesetDir, pre); err != nil {
					return err
				}
				fmt.Fprintf(out, "\nPrereleased %d package(s) (tag %q); changesets kept.\n", len(plan), pre.Tag)
			default: // Normal, Exit
				for _, cs := range changesets {
					_ = os.Remove(filepath.Join(ws.ChangesetDir, cs.ID+".md"))
				}
				if mode == planner.ModeExit {
					_ = prestate.Remove(ws.ChangesetDir)
					fmt.Fprintf(out, "\nGraduated %d package(s) to stable; exited prerelease mode.\n", len(plan))
				} else {
					fmt.Fprintf(out, "\nVersioned %d package(s); removed %d changeset(s).\n", len(plan), len(changesets))
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "print the plan without writing files")
	f.StringVar(&snapshotTag, "snapshot", "", "create a snapshot release (optional tag)")
	f.Lookup("snapshot").NoOptDefVal = " " // allow bare --snapshot (no tag)
	f.StringVar(&snapshotTemplate, "snapshot-template", "", "snapshot suffix template ({tag}/{commit}/{datetime}/{timestamp})")
	return cmd
}

// execRunner adapts os/exec to the injectable Runner seam shared by the
// changelog resolver and the formatter dispatch.
func execRunner(cmd *cobra.Command) func(dir, name string, args ...string) (string, error) {
	return func(dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(cmd.Context(), name, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
}
