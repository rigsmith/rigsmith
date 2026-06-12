package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newRebuildCmd is not a single native command, so it sequences clean → build.
// (rebuild is registered via verbCmd in root.go for the help listing; this is
// the actual runner, dispatched from there when verb == "rebuild".)
//
// Implemented as a special case in verbCmd rather than a DevCommands entry
// because "rebuild" has no single argv across ecosystems.
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
