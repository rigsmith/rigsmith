package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/spf13/cobra"
)

// NewVersionCmd builds the `version` command, including snapshot (--snapshot) and
// prerelease (driven by .changeset/pre.json) modes.
func NewVersionCmd() *cobra.Command {
	var (
		dryRun           bool
		snapshotTag      string
		snapshotTemplate string
		independent      bool
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

			// Discover first: commit-based versioning needs the package set to
			// attribute commits before it can synthesize changesets.
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			changesets, fromCommits, err := ws.LoadChangesets(cmd.Context(), pkgs)
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

			// Split the run's changesets into consumed (released → deleted
			// afterwards) and kept (every named package ignored → left for a
			// future run). Mixed and unknown-package changesets are hard
			// errors before anything is written, matching @changesets.
			consumed, kept, err := planner.PartitionChangesets(active, pkgs, ws.Config)
			if err != nil {
				return err
			}

			// changelog-git / changelog-github enrichment: decorate each summary's
			// first line with its commit (and PR/author) before planning. For
			// on-disk changesets that means finding the commit that ADDED the
			// changeset file; for commit-derived changesets the source commit is
			// already in hand (cs.Commit), so we decorate straight from it — better
			// provenance, no archaeology. Every lookup failure degrades to an
			// undecorated line — enrichment never fails the run.
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

			// Contributors section: resolve each changeset's author (from its
			// source commit in commit mode, or the commit that added the file in
			// changeset mode), then attach the per-package list — de-duplicated,
			// run through the exclude/bot filter, sorted — to its module. Works
			// for both versioning sources. Skipped on dry runs (above) since no
			// changelog is written.
			if ws.Config.Contributors.Enabled {
				attachContributors(cmd, ws, plan, active, setting)
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

			// Apply every manifest and changelog write under a file transaction:
			// if any package fails partway, roll the already-written files back to
			// their pre-run contents. Otherwise a mid-loop failure left earlier
			// packages bumped on disk while their changesets stayed (they're only
			// consumed below, after this loop), so a re-run bumped them a second
			// time from the already-bumped versions.
			txn := newFileTxn()
			var changelogPaths []string
			for _, m := range plan {
				if eco, ok := ws.EcosystemFor(ecoOf[m.Name]); ok {
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
						Package:           plugin.Package{Name: m.Name, ManifestPath: m.ManifestPath, VersionFile: m.VersionFile},
						NewVersion:        m.ResolvedVersion(),
						DependencyUpdates: m.DepUpdates,
					}
					if err := eco.SetVersion(cmd.Context(), req); err != nil {
						txn.rollback()
						return fmt.Errorf("set version for %s: %w", m.Name, err)
					}
				}
				if m.RangeOnly {
					continue // "none" release: ranges rewritten, no version bump, no changelog
				}
				entry, err := gen.Render(cmd.Context(), planner.ModuleToRequest(m))
				if err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				pkgDir := filepath.Dir(filepath.Join(ws.Root, m.ManifestPath))
				changelogPath := filepath.Join(pkgDir, changelog.FileName)
				if err := txn.guard(changelogPath); err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				if err := changelog.WriteEntry(pkgDir, m.DisplayName, entry); err != nil {
					txn.rollback()
					return fmt.Errorf("changelog for %s: %w", m.Name, err)
				}
				changelogPaths = append(changelogPaths, changelogPath)
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
			// consumed changesets are removed (or, in pre mode, recorded);
			// ignored-only ones stay on disk, as Node leaves them.
			switch mode {
			case planner.ModeSnapshot:
				// Snapshot consumes changesets like a normal run (verified against
				// @changesets v3.0.0-next.5); the run is throwaway because the
				// working-tree changes are never committed.
				removed := removeConsumedFiles(ws.ChangesetDir, consumed)
				fmt.Fprintf(out, "\nSnapshot-versioned %d package(s)%s.\n", len(plan), removedSuffix(removed, fromCommits))
			case planner.ModePre:
				for _, cs := range consumed {
					pre.Changesets = append(pre.Changesets, cs.ID)
				}
				if err := prestate.Write(ws.ChangesetDir, pre); err != nil {
					return err
				}
				fmt.Fprintf(out, "\nPrereleased %d package(s) (tag %q); changesets kept.\n", len(plan), pre.Tag)
			default: // Normal, Exit
				removed := removeConsumedFiles(ws.ChangesetDir, consumed)
				if mode == planner.ModeExit {
					_ = prestate.Remove(ws.ChangesetDir)
					fmt.Fprintf(out, "\nGraduated %d package(s) to stable; exited prerelease mode.\n", len(plan))
				} else {
					fmt.Fprintf(out, "\nVersioned %d package(s)%s.\n", len(plan), removedSuffix(removed, fromCommits))
				}
			}
			if len(kept) > 0 {
				fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("kept %d changeset(s) naming only ignored packages.", len(kept))))
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
	f.StringVar(&snapshotTag, "snapshot", "", "create a snapshot release (optional tag)")
	f.Lookup("snapshot").NoOptDefVal = " " // allow bare --snapshot (no tag)
	f.StringVar(&snapshotTemplate, "snapshot-template", "", "snapshot suffix template ({tag}/{commit}/{datetime}/{timestamp})")
	f.BoolVar(&independent, "independent", false, "version each package on its own changesets, writing inline (overrides a shared version file)")
	return cmd
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
func removeConsumedFiles(changesetDir string, consumed []*changeset.Changeset) int {
	removed := 0
	for _, cs := range consumed {
		if err := os.Remove(filepath.Join(changesetDir, cs.ID+".md")); err == nil {
			removed++
		}
	}
	return removed
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
