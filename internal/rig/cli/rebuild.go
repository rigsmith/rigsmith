package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// runRebuildVerb dispatches `rig rebuild`. Because rebuild sequences clean →
// build (no single argv), it can't ride the generic project picker the other dev
// verbs share, so it carries its own: an explicit [project] scopes the rebuild to
// that package; a bare rebuild runs the root primary when there is one, and
// otherwise — a workspace root whose packages live only in subdirs, or with
// -i/--interactive — opens a package picker whose "All packages" rebuilds each in
// dependency order.
func runRebuildVerb(cmd *cobra.Command, root string, args []string, forcePick bool) error {
	// An explicit project arg scopes the rebuild to that package's dir + ecosystem.
	if len(args) > 0 {
		ts := discoverWorkspace(cdContext(cmd), root, excludeFor(root))
		t, ok := matchTarget(ts, args[0])
		if !ok {
			return fmt.Errorf("no workspace package %q to rebuild", args[0])
		}
		return runRebuild(cmd, t.Eco, t.Dir, args[1:])
	}

	cwd, _ := os.Getwd()
	eco, ecoErr := resolvePrimary(cwd, root)
	// A resolvable root primary rebuilds in place — unless -i forces the picker.
	if ecoErr == nil && !forcePick {
		return runRebuild(cmd, eco, root, nil)
	}

	// No single primary (or -i): offer the workspace packages as rebuild targets.
	tasks := rebuildTasks(cmd, root)
	switch {
	case len(tasks) == 0:
		if ecoErr != nil {
			return ecoErr // nothing at the root and no subpackages — surface why
		}
		return runRebuild(cmd, eco, root, nil)
	case !forcePick && len(tasks) == 1:
		t := tasks[0]
		return runRebuild(cmd, t.eco, t.dir, nil)
	case !interactive():
		if forcePick {
			return fmt.Errorf("-i/--interactive needs an interactive terminal; run `rig rebuild <project>`")
		}
		return fmt.Errorf("no single rebuild target here — this is a workspace root with %d packages; run `rig rebuild <project>`", len(tasks))
	}

	switch choice := pickWorkspaceVerbTarget("rebuild", tasks, true); choice {
	case pickCancel:
		return nil
	case pickAll:
		return rebuildAll(cmd, tasks)
	default:
		t := tasks[choice]
		return runRebuild(cmd, t.eco, t.dir, nil)
	}
}

// rebuildTasks lists every workspace package as a rebuild target in dependency
// order. Unlike the dev verbs, a rebuild task carries no argv — runRebuild needs
// only the ecosystem + dir.
func rebuildTasks(cmd *cobra.Command, root string) []allTask {
	targets := topoSort(filterTargets(discoverWorkspace(cdContext(cmd), root, excludeFor(root)), ""))
	tasks := make([]allTask, 0, len(targets))
	for _, t := range targets {
		rel, err := filepath.Rel(root, t.Dir)
		if err != nil {
			rel = t.Dir
		}
		tasks = append(tasks, allTask{name: t.Name, eco: t.Eco, dir: t.Dir, rel: filepath.ToSlash(rel)})
	}
	return tasks
}

// rebuildAll rebuilds every task in turn, aborting on the first failure — the
// "All packages" choice. rebuild's clean → build doesn't fit the --all dashboard
// (which streams a single command per package), so it runs sequentially.
func rebuildAll(cmd *cobra.Command, tasks []allTask) error {
	out := cmd.OutOrStdout()
	for _, t := range tasks {
		fmt.Fprintln(out, dimStyle.Render(fmt.Sprintf("· %s (%s)", t.name, t.eco)))
		if err := runRebuild(cmd, t.eco, t.dir, nil); err != nil {
			return fmt.Errorf("rebuild in %s: %w", t.name, err)
		}
	}
	return nil
}

// runRebuild rebuilds one target by sequencing clean → build (rebuild is not a
// single native command). Implemented as a special case rather than a
// DevCommands entry because "rebuild" has no single argv across ecosystems.
func runRebuild(cmd *cobra.Command, eco, root string, args []string) error {
	// .NET parity: the .NET rig's rebuild deletes every discovered project's
	// bin/obj before building (`dotnet clean` leaves NuGet/analyzer droppings
	// behind). Scoped to solution projects — vendored trees are never touched.
	if eco == detect.DotNet {
		cfg, _ := config.LoadMerged(root)
		projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
		var skip []string
		if cfg.Rebuild != nil {
			skip = cfg.Rebuild.Skip
		}
		rebuildRemoveBinObj(cmd.OutOrStdout(), root, projects, skip, dryRun)
	}

	for _, verb := range []string{"clean", "build"} {
		// Node clean is a project-defined script, not a canonical command; skip it
		// in rebuild unless the project actually provides one (build alone is fine).
		if verb == "clean" && eco == detect.Node && !detect.NodeHasScript(root, "clean") {
			continue
		}
		argv, ok := detect.CommandFor(eco, verb, root)
		if !ok {
			if verb == "clean" {
				continue // some ecosystems have no clean; build alone is fine
			}
			return fmt.Errorf("verb %q has no mapping for ecosystem %q", verb, eco)
		}
		if verb == "build" {
			argv = append(argv, args...)
		}
		if err := runCommand(cmd, root, argv); err != nil {
			return err
		}
	}
	return nil
}

// rebuildTargetDirs is the bin/obj to remove, scoped to the discovered
// solution projects (+ the root). Scoping is the convention that makes
// `rebuild.skip` unnecessary: vendored trees that aren't solution projects are
// never touched. The optional skip-list still filters further. Mirrors the
// .NET rig's RebuildVerb.TargetDirs.
func rebuildTargetDirs(root string, projects []detect.ProjectInfo, skip []string) []string {
	var dirs []string
	for _, p := range projects {
		dirs = appendBinObj(dirs, filepath.Dir(p.FullPath))
	}
	dirs = appendBinObj(dirs, root)

	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		rel, err := filepath.Rel(root, d)
		if err != nil {
			rel = d
		}
		if !rebuildIsSkipped(rel, skip) {
			out = append(out, d)
		}
	}
	return out
}

// appendBinObj appends dir's existing bin/obj directories to into.
func appendBinObj(into []string, dir string) []string {
	for _, name := range []string{"bin", "obj"} {
		d := filepath.Join(dir, name)
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			into = append(into, d)
		}
	}
	return into
}

// rebuildRemoveBinObj deletes the targeted bin/obj directories, reporting how
// many were removed. On dryRun it only lists them (deleting nothing). Mirrors
// the removal phase of the .NET rig's RebuildVerb.Execute, including its
// data-loss guards: never delete outside the repo, never follow a symlink out
// of it.
func rebuildRemoveBinObj(out io.Writer, root string, projects []detect.ProjectInfo, skip []string, dryRun bool) int {
	targets := rebuildTargetDirs(root, projects, skip)

	if dryRun {
		fmt.Fprintf(out, "Dry run — would remove %d bin/obj director%s:\n", len(targets), plural(len(targets), "y", "ies"))
		for _, dir := range targets {
			fmt.Fprintf(out, "  %s\n", relToRoot(root, dir))
		}
		return 0
	}

	removed := 0
	for _, dir := range targets {
		// Data-loss guard: never delete outside the repo, and never follow a
		// symlink out of it (a stray project path or a symlinked bin/obj could
		// otherwise aim the recursive delete elsewhere).
		if !rebuildIsWithinRoot(root, dir) {
			fmt.Fprintf(out, "  refusing %s — outside the repo\n", dir)
			continue
		}
		info, err := os.Lstat(dir)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(out, "  skipping symlink %s\n", dir)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(out, "  skipped %s: %v\n", dir, err)
			continue
		}
		removed++
	}
	fmt.Fprintf(out, "removed %d bin/obj director%s\n", removed, plural(removed, "y", "ies"))
	return removed
}

// relToRoot renders dir relative to root for display, falling back to dir.
func relToRoot(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil {
		return rel
	}
	return dir
}

// plural picks the singular/plural suffix for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
