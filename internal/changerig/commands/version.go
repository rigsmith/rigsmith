package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/changelog"
	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/mdfmt"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/prestate"
	"github.com/rigsmith/rigsmith/core/versionstate"
	"github.com/spf13/cobra"
)

// NewVersionCmd builds the `version` command, including snapshot (--snapshot) and
// prerelease (driven by .changeset/pre.json) modes.
func NewVersionCmd() *cobra.Command {
	var (
		dryRun           bool
		sinceRef         string
		ignoreFlag       []string
		onlyFlag         []string
		releaseAs        []string
		showChangelog    bool
		snapshotTag      string
		snapshotTemplate string
		independent      bool
		yes              bool
		noStamp          bool
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
			// --changelog is a preview: render the notes to stdout, write nothing.
			if showChangelog {
				dryRun = true
			}
			// A branch's share of a release is something to preview, never to
			// write: versioning it would drop the base branch's changes.
			if sinceRef != "" && !dryRun {
				return errors.New("--since only narrows a preview: use it with --changelog or --dry-run")
			}
			ws, err := Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Discover first: commit-based versioning needs the package set to
			// attribute commits before it can synthesize changesets.
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			pkgByName := make(map[string]plugin.Package, len(pkgs))
			for _, p := range pkgs {
				pkgByName[p.Name] = p
			}
			// --only, shiprig's own: version just these packages, and treat
			// every other one as ignored for this run on top of the config's
			// ignore (unlike --ignore, it combines with it). What the named
			// packages must move with (status --output's group) has to be
			// named too; the refusals below say so.
			configIgnore := slices.Clone(ws.Config.Ignore)
			if len(onlyFlag) > 0 {
				if len(ignoreFlag) > 0 {
					return errors.New("--only and --ignore can't be combined: --only already leaves out every package it doesn't name")
				}
				var unknown []string
				only := map[string]bool{}
				for _, name := range onlyFlag {
					if _, ok := pkgByName[name]; !ok {
						unknown = append(unknown, name)
					}
					only[name] = true
				}
				if len(unknown) > 0 {
					return fmt.Errorf("--only names %s, which is not in the workspace; is it misspelled?", strings.Join(unknown, ", "))
				}
				for _, p := range pkgs {
					if !only[p.Name] {
						ws.Config.Ignore = append(ws.Config.Ignore, p.Name)
					}
				}
			}
			// --ignore, as @changesets has it: exact names, and not alongside
			// an `ignore` in the config.
			if len(ignoreFlag) > 0 {
				if len(ws.Config.Ignore) > 0 {
					return errors.New("--ignore can't be used while `ignore` is set in the config: use one or the other, as @changesets does")
				}
				var unknown []string
				for _, name := range ignoreFlag {
					if _, ok := pkgByName[name]; !ok {
						unknown = append(unknown, name)
					}
				}
				if len(unknown) > 0 {
					return fmt.Errorf("--ignore names %s, which is not in the workspace; is it misspelled?", strings.Join(unknown, ", "))
				}
				ws.Config.Ignore = ignoreFlag
			}
			if msgs := planner.SkippedDependents(pkgs, ws.Config, len(ignoreFlag) > 0); len(msgs) > 0 {
				if len(onlyFlag) > 0 {
					return fmt.Errorf("--only leaves out packages the named ones move with:\n%s\npass each package's whole release group (`status --output` lists them)", strings.Join(msgs, "\n"))
				}
				return errors.New(strings.Join(msgs, "\n"))
			}
			// Whether the new versions are written into manifests at all. Off by
			// flag or config, the numbers are computed, cascaded and recorded
			// beside the changesets instead; and a stackspace member's manifest
			// is never written, whatever the flag says (ws.Stamps).
			stamp := !noStamp && ws.Config.StampEnabled()
			if sinceRef != "" {
				if _, err := ws.NarrowSince(cmd.Context(), sinceRef); err != nil {
					return err
				}
			}
			changesets, fromCommits, err := ws.LoadChangesets(cmd.Context(), pkgs)
			if err != nil {
				return err
			}
			pre, err := prestate.Read(ws.ChangesetDir)
			if err != nil {
				return err
			}
			// A snapshot is throwaway and consumes what it plans from, so it
			// must never take the graduating changesets waiting in pre/: the
			// stable run after `pre exit` still needs them.
			if !cmd.Flags().Changed("snapshot") {
				if changesets, err = withGraduating(ws, changesets, pre); err != nil {
					return err
				}
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
			// counter still advances from the current version. Consumed ones live
			// in .changeset/pre/, which LoadChangesets does not read; the filter
			// is for a prerelease begun under the v2 layout.
			active := changesets
			if mode == planner.ModePre {
				active = nil
				for _, cs := range changesets {
					if !pre.Contains(cs.ID) {
						active = append(active, cs)
					}
				}
			}

			// A prerelease begun under v2 lists its consumed changesets in
			// pre.json and leaves them at the top level. Move them into pre/ up
			// front, so a run with nothing new still migrates instead of
			// stopping at the empty check below.
			if mode == planner.ModePre && len(pre.Changesets) > 0 && !dryRun {
				if _, err := pre.MoveToPre(ws.ChangesetDir, nil); err != nil {
					return fmt.Errorf("migrating prerelease changesets into .changeset/pre/: %w", err)
				}
				if err := prestate.Write(ws.ChangesetDir, pre); err != nil {
					return err
				}
			}

			if len(active) == 0 && mode != planner.ModeExit {
				// A failure, as in @changesets v3: a release pipeline that reaches
				// `version` with nothing to release has gone wrong upstream of it.
				return errors.New("no unreleased changesets found — nothing to version")
			}

			// Split the run's changesets into consumed (released → deleted
			// afterwards) and kept (every named package ignored → left for a
			// future run). Mixed and unknown-package changesets are hard
			// errors before anything is written, matching @changesets.
			if len(onlyFlag) > 0 {
				if err := checkWholeGroups(onlyFlag, active, pkgs, ecoOf, ws.Config, configIgnore, independent); err != nil {
					return err
				}
			}
			consumed, kept, err := planner.PartitionChangesets(active, pkgs, ws.Config)
			if err != nil {
				if len(onlyFlag) > 0 {
					return fmt.Errorf("%w\n--only has to name every package a changeset names together: pass each package's whole release group (`status --output` lists them)", err)
				}
				return err
			}

			// changelog-git / changelog-github enrichment: decorate each summary's
			// first line with its commit (and PR/author) before planning. For
			// on-disk changesets that means finding the commit that ADDED the
			// changeset file; for commit-derived changesets the source commit is
			// already in hand (cs.Commit), so we decorate straight from it — better
			// provenance, no archaeology. Every lookup failure degrades to an
			// undecorated line — enrichment never fails the run.
			// Judged on the AUTHORED body, before the enrichment below rewrites
			// each summary in place. A changelog-github run injects a repo URL,
			// and `acme/widgets` in that URL would read as a mention of a package
			// named widgets — so version would fall silent exactly where status
			// warned. Printed later, after the plan; only the reading happens here.
			unmentioned := FindUnmentioned(active, ws.Config)

			setting := changelog.ParseSetting(ws.Config)
			if setting.Kind != changelog.KindDefault {
				fileIDs := make([]string, 0, len(active))
				commitIDs := map[string]string{}
				for _, cs := range active {
					if cs.Commit != "" {
						commitIDs[cs.ID] = cs.Commit
					} else {
						fileIDs = append(fileIDs, cs.ID)
					}
				}
				infos := changelog.Resolve(fileIDs, setting, ws.Root, execRunner(cmd))
				for id, info := range changelog.ResolveFromCommits(commitIDs, setting, ws.Root, execRunner(cmd)) {
					infos[id] = info
				}
				for _, cs := range active {
					if info, ok := infos[cs.ID]; ok {
						cs.Summary = changelog.RenderLine(cs.Summary, setting, &info)
					}
				}
			}

			// --independent forces every package independent for this run,
			// overriding both the top-level strategy and any per-ecosystem block.
			// Otherwise honor per-ecosystem `versionStrategy` overrides (a package's
			// ecosystem block wins over the top-level VersionStrategy).
			if independent {
				ws.Config.VersionStrategy = config.Independent
			} else {
				ws.Config.PerPackageStrategy = ws.Config.StrategyByPackage(ecoOf)
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
				if ws.Graduates() {
					plan = planner.GraduatePrereleases(plan, pkgs)
				}
			}

			if len(plan) == 0 {
				fmt.Fprintln(out, DimStyle.Render("Nothing to version."))
				return nil
			}

			// --release-as: the override prompt's answer given up front, so a
			// preview shows it and CI can set it. Normal releases only, as the
			// prompt: a prerelease or snapshot sets its own suffix.
			if len(releaseAs) > 0 {
				if mode != planner.ModeNormal {
					return errors.New("--release-as applies to a normal release, not a prerelease or snapshot")
				}
				if err := applyReleaseAs(out, plan, releaseAs); err != nil {
					return err
				}
				if err := checkOverriddenDependents(plan, pkgs); err != nil {
					return err
				}
				planner.RefreshDependencies(plan)
			}

			PrintPlan(out, plan, false)

			// Before any changelog is written, and before the override prompt —
			// this is the last moment splitting is cheap. `status` is the better
			// place to see it, but someone who never runs status still gets one
			// chance here.
			if len(unmentioned) > 0 {
				fmt.Fprintln(out)
				PrintUnmentioned(out, unmentioned, cmd.Root().Name())
			}

			// Resolve the changelog generator once: both the --changelog dry-run
			// preview and the real write below render through it, so the preview is
			// byte-identical to the file content. The three @changesets generators
			// (default, changelog-git, changelog-github) all render the default
			// layout — git/github only decorate the release lines (done above).
			// Anything else resolves as an external plugin.
			genSpec := ws.Config.ChangelogSpec()
			if setting.Kind != changelog.KindDefault {
				genSpec = "default"
			}
			gen, _ := plugin.ResolveChangelogGenerator(genSpec, ws.Root, planner.BuiltinsScoped(ws.Config.Groups(), ws.Config.Scopes()))

			// Contributors section: resolve each changeset's author (from its
			// source commit in commit mode, or the commit that added the file in
			// changeset mode), then attach the per-package list — de-duplicated,
			// run through the exclude/bot filter, sorted — to its module. Resolving
			// authors touches git, so only do it when a changelog will be rendered:
			// a real run, or a --changelog preview.
			if (showChangelog || !dryRun) && ws.Config.Contributors.Enabled {
				attachContributors(cmd, ws, plan, active, setting)
			}

			if dryRun {
				if showChangelog {
					printChangelogPreview(out, cmd.Context(), gen, plan, ws.Config.Scopes())
				}
				fmt.Fprintln(out, DimStyle.Render("\n(dry run — no files written)"))
				return nil
			}

			// Interactive version override (release-it style): offer each releasing
			// package's computed next version and let the user accept or override it.
			// Only for a normal release on a real terminal — snapshot/prerelease set
			// their own version suffixes, and --yes / non-interactive runs accept the
			// computed plan as-is.
			if !yes && len(releaseAs) == 0 && mode == planner.ModeNormal && Interactive() {
				changed, err := promptVersionOverrides(out, plan)
				if err != nil {
					return err
				}
				if changed {
					if err := checkOverriddenDependents(plan, pkgs); err != nil {
						return err
					}
					// Dependents' ranges and changelog lines follow the chosen
					// versions.
					planner.RefreshDependencies(plan)
					fmt.Fprintln(out)
					PrintPlan(out, plan, false)
				}
			}

			// Apply every manifest and changelog write under a file transaction:
			// if any package fails partway, roll the already-written files back to
			// their pre-run contents. Otherwise a mid-loop failure left earlier
			// packages bumped on disk while their changesets stayed (they're only
			// consumed below, after this loop), so a re-run bumped them a second
			// time from the already-bumped versions.
			txn := newFileTxn()
			var changelogPaths []string
			// The versions the run computed but did not write anywhere in the
			// tree — no stamping, or a manifest that is not this repository's
			// to write — go to .changeset/versions.json, where the next plan
			// reads them back. A snapshot is throwaway and records nothing.
			recorded, err := versionstate.Read(ws.ChangesetDir)
			if err != nil {
				return fmt.Errorf("reading %s: %w", filepath.Join(ws.ChangesetDir, versionstate.FileName), err)
			}
			var unstamped []string
			stateChanged := false
			for _, m := range plan {
				pkg := pkgByName[m.Name]
				eco, ok := ws.EcosystemFor(ecoOf[m.Name])
				switch {
				case !ok:
				case !(stamp && ws.Stamps(pkg)):
					// Not written into the tree: the number is recorded instead,
					// and named below so nobody looks for it in the manifest.
					if !m.RangeOnly {
						if mode != planner.ModeSnapshot {
							recorded.Set(m.Name, m.ResolvedVersion())
							stateChanged = true
						}
						unstamped = append(unstamped, m.Name)
					}
				default:
					// Guard both candidate version targets (a shared VersionFile and
					// the manifest) before mutating either.
					if err := txn.guard(filepath.Join(ws.Root, m.ManifestPath)); err != nil {
						txn.rollback()
						return fmt.Errorf("set version for %s: %w", m.Name, err)
					}
					if m.VersionFile != "" {
						if err := txn.guard(filepath.Join(ws.Root, m.VersionFile)); err != nil {
							txn.rollback()
							return fmt.Errorf("set version for %s: %w", m.Name, err)
						}
					}
					req := plugin.SetVersionRequest{
						RepoRoot:          ws.Root,
						Package:           plugin.Package{Name: m.Name, Dir: filepath.Dir(m.ManifestPath), ManifestPath: m.ManifestPath, VersionFile: m.VersionFile},
						NewVersion:        m.ResolvedVersion(),
						DependencyUpdates: m.DepUpdates,
					}
					if err := eco.SetVersion(cmd.Context(), req); err != nil {
						txn.rollback()
						return fmt.Errorf("set version for %s: %w", m.Name, err)
					}
					// The manifest is the version's home again: a record left
					// by an earlier unstamped run has been bumped from and is
					// reconciled away, so the two cannot disagree later. A
					// snapshot stamps a throwaway number and keeps the record.
					if mode != planner.ModeSnapshot && recorded.Get(m.Name) != "" {
						recorded.Delete(m.Name)
						stateChanged = true
					}
				}
				// The release record: every release, stamped or not. A
				// snapshot is throwaway, and a range-only rewrite releases
				// nothing.
				if ws.Config.Versioning.Record && !m.RangeOnly && mode != planner.ModeSnapshot {
					recorded.SetReleased(m.Name, m.ResolvedVersion())
					stateChanged = true
				}
				if m.RangeOnly {
					continue // "none" release: ranges rewritten, no version bump, no changelog
				}
				entry, err := gen.Render(cmd.Context(), planner.ModuleToRequestScoped(m, ws.Config.Scopes()))
				if err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				// A shared file (a stackspace's root CHANGELOG.md) gets a
				// section per package rather than a title-based write, which
				// would land under whichever package came first.
				changelogPath, section := ws.ChangelogFor(pkg)
				if section != "" {
					if err := txn.guard(changelogPath); err != nil {
						txn.rollback()
						return fmt.Errorf("changelog for %s: %w", m.Name, err)
					}
					if err := changelog.WriteSection(changelogPath, section, entry); err != nil {
						txn.rollback()
						return fmt.Errorf("changelog for %s: %w", m.Name, err)
					}
					if !slices.Contains(changelogPaths, changelogPath) {
						changelogPaths = append(changelogPaths, changelogPath)
					}
					continue
				}
				if err := txn.guard(changelogPath); err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				if err := changelog.WriteEntry(filepath.Dir(changelogPath), m.DisplayName, entry); err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				changelogPaths = append(changelogPaths, changelogPath)
			}
			if stateChanged {
				statePath := filepath.Join(ws.ChangesetDir, versionstate.FileName)
				if err := txn.guard(statePath); err != nil {
					txn.rollback()
					return fmt.Errorf("recording versions: %w", err)
				}
				if err := versionstate.Write(ws.ChangesetDir, recorded); err != nil {
					txn.rollback()
					return fmt.Errorf("recording versions: %w", err)
				}
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

			// Changeset disposal + pre-state bookkeeping per mode. Only the
			// consumed changesets are removed (or, in pre mode, moved into
			// .changeset/pre/); ignored-only ones stay on disk, as Node leaves them.
			switch mode {
			case planner.ModeSnapshot:
				// Snapshot consumes changesets like a normal run (verified against
				// @changesets 3.0.3); the run is throwaway because the
				// working-tree changes are never committed.
				removed, rmErr := removeConsumedFiles(ws.ChangesetDir, consumed)
				if rmErr != nil {
					fmt.Fprintln(out, DimStyle.Render("warn could not remove a consumed changeset: "+rmErr.Error()))
				}
				fmt.Fprintf(out, "\nSnapshot-versioned %d package(s)%s.\n", len(plan), removedSuffix(removed, fromCommits))
			case planner.ModePre:
				ids := make([]string, 0, len(consumed))
				for _, cs := range consumed {
					ids = append(ids, cs.ID)
				}
				// The move and pre.json join the transaction: if either fails,
				// the manifests and changelogs are restored too, so a retry
				// doesn't bump the same changesets twice.
				undo, err := pre.MoveToPre(ws.ChangesetDir, ids)
				if err != nil {
					txn.rollback()
					return fmt.Errorf("moving changesets into .changeset/pre/: %w", err)
				}
				prePath := filepath.Join(ws.ChangesetDir, "pre.json")
				if err := txn.guard(prePath); err != nil {
					undo()
					txn.rollback()
					return err
				}
				if err := prestate.Write(ws.ChangesetDir, pre); err != nil {
					undo()
					txn.rollback()
					return err
				}
				fmt.Fprintf(out, "\nPrereleased %d package(s) (tag %q); changesets kept.\n", len(plan), pre.Tag)
			default: // Normal, Exit
				removed, rmErr := removeConsumedFiles(ws.ChangesetDir, consumed)
				if mode == planner.ModeExit {
					// Stop while pre.json still says "exit": returning an
					// undeleted consumed changeset to the top level would
					// release it a second time.
					if rmErr != nil {
						return fmt.Errorf("removing graduated changesets (prerelease state left in place; delete them and re-run): %w", rmErr)
					}
					// A graduating changeset that wasn't consumed (its package
					// is now ignored) goes back to the top level, where later
					// runs look, before pre.json and pre/ are dropped.
					if err := prestate.ReturnToTop(ws.ChangesetDir); err != nil {
						return fmt.Errorf("returning unreleased changesets from .changeset/pre/: %w", err)
					}
					prestate.RemoveDir(ws.ChangesetDir)
					if err := prestate.Remove(ws.ChangesetDir); err != nil {
						return fmt.Errorf("leaving prerelease mode: %w", err)
					}
					fmt.Fprintf(out, "\nGraduated %d package(s) to stable; exited prerelease mode.\n", len(plan))
				} else {
					if rmErr != nil {
						fmt.Fprintln(out, DimStyle.Render("warn could not remove a consumed changeset: "+rmErr.Error()))
					}
					fmt.Fprintf(out, "\nVersioned %d package(s)%s.\n", len(plan), removedSuffix(removed, fromCommits))
				}
			}
			if len(kept) > 0 {
				msg := fmt.Sprintf("kept %d changeset(s) naming only ignored packages.", len(kept))
				if len(onlyFlag) > 0 {
					// Under --only they're waiting, not ignored: say for what.
					msg = fmt.Sprintf("left %d changeset(s) for a later run: they name only packages outside --only (%s).", len(kept), previewNames(keptPackages(kept), 4))
				}
				fmt.Fprintln(out, DimStyle.Render(msg))
			}
			if len(unstamped) > 0 {
				why := "--no-stamp"
				switch {
				case !ws.Config.StampEnabled():
					why = "versioning.stamp is off"
				case stamp:
					why = "no version in the tree, or a stackspace member's manifest"
				}
				where := "recorded in .changeset/" + versionstate.FileName
				if mode == planner.ModeSnapshot {
					where = "not recorded (snapshot)"
				}
				fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("not stamped (%s): %s — %s.", why, strings.Join(unstamped, ", "), where)))
			}

			// Auto-commit the version bumps + changelogs + changeset deletions when
			// the `commit` config key is enabled. Snapshot runs are throwaway (their
			// working-tree changes are never meant to be committed), so they opt out.
			if mode != planner.ModeSnapshot && ws.Config.CommitEnabled() {
				committed, err := gitutil.StageAndCommit(cmd.Context(), ws.Root, "Version Packages")
				switch {
				case err != nil:
					fmt.Fprintln(out, DimStyle.Render("warn could not commit: "+err.Error()))
				case committed:
					fmt.Fprintln(out, DimStyle.Render("committed version changes."))
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "print the plan without writing files")
	f.BoolVar(&showChangelog, "changelog", false, "preview each releasing package's rendered changelog notes (implies --dry-run; writes nothing)")
	f.StringVar(&sinceRef, "since", "", "preview only what the branch adds since this git ref: its changesets and commits (needs --changelog or --dry-run)")
	f.StringArrayVar(&ignoreFlag, "ignore", nil, "leave this package out of the run (repeatable; not with `ignore` in the config)")
	f.StringArrayVar(&onlyFlag, "only", nil, "version only this package, leaving every other one for a later run (repeatable; name each package's whole group from `status --output`)")
	_ = cmd.RegisterFlagCompletionFunc("only", completePackageNames)
	// Completion offers the workspace's package names, the only values --ignore
	// accepts.
	_ = cmd.RegisterFlagCompletionFunc("ignore", completePackageNames)
	f.StringVar(&snapshotTag, "snapshot", "", "create a snapshot release (optional tag)")
	f.Lookup("snapshot").NoOptDefVal = " " // allow bare --snapshot (no tag)
	f.StringVar(&snapshotTemplate, "snapshot-template", "", "snapshot suffix template ({tag}/{commit}/{datetime}/{timestamp})")
	f.BoolVar(&independent, "independent", false, "version each package on its own changesets, writing inline (overrides a shared version file)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the computed versions; skip the interactive version-override prompt")
	f.StringArrayVar(&releaseAs, "release-as", nil, "release a package at this exact version, <package>=<version> (or a bare <version> when one version is releasing); repeatable, and skips the prompt")
	_ = cmd.RegisterFlagCompletionFunc("release-as", completeReleaseAs)
	f.BoolVar(&noStamp, "no-stamp", false, "compute and record the versions (.changeset/versions.json) without writing them into any manifest")
	return cmd
}

// printChangelogPreview renders each releasing package's changelog entry — the
// exact markdown `version` would prepend to its CHANGELOG.md — to out, writing
// nothing. It routes through the same generator the real run uses, so the
// preview is byte-identical to the written entry (honoring changelog groups,
// lockstep grouping baked into the plan, and the contributors section attached
// above). "none" releases (RangeOnly) get no changelog, so they are skipped.
func printChangelogPreview(out io.Writer, ctx context.Context, gen plugin.ChangelogGenerator, plan []*planner.Module, scopeOrder []string) {
	for _, m := range plan {
		if m.RangeOnly {
			continue
		}
		entry, err := gen.Render(ctx, planner.ModuleToRequestScoped(m, scopeOrder))
		if err != nil {
			fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("\n  (changelog render failed for %s: %v)", m.Name, err)))
			continue
		}
		// Mirror WriteEntry's file layout: the "# DisplayName" title above the
		// rendered entry, the same heading a fresh CHANGELOG.md receives.
		fmt.Fprintf(out, "\n%s\n\n%s", HeaderStyle.Render("# "+m.DisplayName), entry)
	}
}

// attachContributors resolves the author behind each active changeset and sets
// each releasing module's Contributors (deduped, excluded, sorted) so the
// builtin changelog generator renders the "Contributors" section. The GitHub
// repo for linking is the changelog-github setting's repo when present, else the
// origin remote's slug — so links work even with the default changelog.
func attachContributors(cmd *cobra.Command, ws *Workspace, plan []*planner.Module, active []*changeset.Changeset, setting changelog.Setting) {
	repo := setting.Repo
	if repo == "" {
		repo = gitutil.GitHubRepoSlug(cmd.Context(), ws.Root)
	}

	ids := make([]string, 0, len(active))
	known := map[string]string{}
	for _, cs := range active {
		ids = append(ids, cs.ID)
		if cs.Commit != "" {
			known[cs.ID] = cs.Commit
		}
	}
	authorsByID := changelog.ResolveAuthors(ids, known, repo, ws.Root, execRunner(cmd))
	section := ws.Config.Contributors.SectionHeading()

	for _, m := range plan {
		// Collect this package's contributors (commit author + any co-authors of
		// every changeset naming it), de-duplicated by identity. The email is the
		// merge key (most stable across name spellings); a later occurrence that
		// carries a GitHub login upgrades an earlier bare entry — so a co-author
		// who is elsewhere a commit author still gets linked.
		byKey := map[string]*plugin.Author{}
		var order []string
		for _, cs := range active {
			if !changesetNames(cs, m.Name) {
				continue
			}
			for _, a := range authorsByID[cs.ID] {
				if ws.Config.Contributors.IsContributorExcluded(a.Login, a.Name, a.Email) {
					continue
				}
				key := contributorKey(a)
				if existing, ok := byKey[key]; ok {
					if existing.Login == "" && a.Login != "" {
						existing.Login = a.Login
					}
					if existing.Name == "" {
						existing.Name = a.Name
					}
					continue
				}
				// Drop the email before it reaches the changelog — it is never rendered.
				byKey[key] = &plugin.Author{Name: a.Name, Login: a.Login}
				order = append(order, key)
			}
		}
		if len(order) == 0 {
			continue
		}
		list := make([]plugin.Author, 0, len(order))
		for _, k := range order {
			list = append(list, *byKey[k])
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		m.Contributors = list
		m.ContributorsSection = section
	}
}

// changesetNames reports whether the changeset names the given package.
func changesetNames(cs *changeset.Changeset, name string) bool {
	for _, n := range cs.ChangedNames() {
		if n == name {
			return true
		}
	}
	return false
}

// contributorKey is the de-duplication key for an author: email when known (the
// most stable identity across name spellings and the only thing a co-author and
// their own commits reliably share), else GitHub login, else name — all lowered.
func contributorKey(a plugin.Author) string {
	switch {
	case a.Email != "":
		return "email:" + strings.ToLower(a.Email)
	case a.Login != "":
		return "login:" + strings.ToLower(a.Login)
	default:
		return "name:" + strings.ToLower(a.Name)
	}
}

// removeConsumedFiles deletes the on-disk changeset file backing each consumed
// changeset, returning how many actually existed. Commit-derived changesets
// have no file (their ID is a commit hash), so they are silently skipped —
// keeping the "removed N changeset(s)" count honest in commits/both mode.
func removeConsumedFiles(changesetDir string, consumed []*changeset.Changeset) (int, error) {
	removed := 0
	var errs []error
	for _, cs := range consumed {
		// A graduating changeset sits in .changeset/pre/, not at the top. A
		// file in neither place (a commit-derived changeset) is not an error.
		for _, dir := range []string{changesetDir, prestate.Dir(changesetDir)} {
			err := os.Remove(filepath.Join(dir, cs.ID+".md"))
			if err == nil {
				removed++
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
				break
			}
		}
	}
	return removed, errors.Join(errs...)
}

// removedSuffix renders the trailing clause of a version summary: how many
// changeset files were removed, or "from commits" when the run had no files to
// remove because its releases came from the commit log.
func removedSuffix(removed int, fromCommits bool) string {
	switch {
	case removed > 0:
		return fmt.Sprintf("; removed %d changeset(s)", removed)
	case fromCommits:
		return " from commits"
	default:
		return ""
	}
}

// fileTxn snapshots files before they are mutated so a multi-file write can be
// rolled back as a unit, keeping the version step atomic: either every manifest
// and changelog update lands or none do.
type fileTxn struct {
	saved map[string]*savedFile
}

type savedFile struct {
	data    []byte
	existed bool
}

func newFileTxn() *fileTxn { return &fileTxn{saved: map[string]*savedFile{}} }

// guard records path's current contents the first time it is seen, so the file
// can be restored later. Call it before mutating path. Recording the same path
// again is a no-op (the first snapshot is the pre-run state).
func (t *fileTxn) guard(path string) error {
	if _, ok := t.saved[path]; ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.saved[path] = &savedFile{existed: false}
			return nil
		}
		return err
	}
	t.saved[path] = &savedFile{data: data, existed: true}
	return nil
}

// rollback restores every guarded file to its recorded state (best effort):
// pre-existing files are rewritten with their original bytes, freshly created
// files are removed.
func (t *fileTxn) rollback() {
	for path, s := range t.saved {
		if s.existed {
			_ = os.WriteFile(path, s.data, 0o644)
		} else {
			_ = os.Remove(path)
		}
	}
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

// completePackageNames completes a flag that takes a package name with the
// workspace's packages.
func completePackageNames(c *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	ws, err := Open()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	pkgs, _, err := ws.Discover(c.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		names = append(names, p.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// checkWholeGroups refuses an --only that names part of a release group: it
// plans the run as if --only weren't there (the config's own ignore only) and
// requires every planned member of each named package's group to be named
// too. The other refusals catch a split changeset or dependency; a fixed or
// linked group, or a shared version file, splits without either.
func checkWholeGroups(only []string, active []*changeset.Changeset, pkgs []plugin.Package, ecoOf map[string]string, cfg *config.Config, configIgnore []string, independent bool) error {
	full := *cfg
	full.Ignore = configIgnore
	if independent {
		full.VersionStrategy = config.Independent
	} else {
		full.PerPackageStrategy = full.StrategyByPackage(ecoOf)
	}
	plan := planner.Plan(active, pkgs, &full)
	groups := planner.ReleaseGroups(plan, active, &full)
	named := map[string]bool{}
	for _, n := range only {
		named[n] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, n := range only {
		g, ok := groups[n]
		if !ok || seen[g] {
			continue
		}
		seen[g] = true
		var left []string
		for member, mg := range groups {
			if mg == g && !named[member] {
				left = append(left, member)
			}
		}
		if len(left) > 0 {
			sort.Strings(left)
			missing = append(missing, fmt.Sprintf("  group %s also needs %s", g, strings.Join(left, ", ")))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("--only names part of a release group:\n%s\npass each package's whole release group (`status --output` lists them)", strings.Join(missing, "\n"))
}

// keptPackages lists the packages the kept changesets name, sorted.
func keptPackages(kept []*changeset.Changeset) []string {
	seen := map[string]bool{}
	var names []string
	for _, cs := range kept {
		for _, r := range cs.Releases {
			if !seen[r.Name] {
				seen[r.Name] = true
				names = append(names, r.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}
