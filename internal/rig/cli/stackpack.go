package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// stackPackDefaultOut is where the packages land when --out is not given,
// relative to the stackspace root. dist/ is what the artifacts protocol names
// as the convention, and keeping to it means a stackspace looks like any other
// repo to whatever consumes the output.
const stackPackDefaultOut = "dist"

// newStackPackCmd builds a member's publishable packages from inside the
// stackspace, where the build overlay is in effect.
//
// This exists because the alternative does not work and does not say so. A
// member consumes its siblings by package identity; the overlay is what points
// those references at the sibling's source. Pack the same projects from a bare
// checkout of a proposed branch and there is no sibling and, often, no feed
// carrying the version the branch pins — restore fails, and NuGet in particular
// can fail without logging a reason. Packing here is the supported answer, and
// having a verb for it means nobody has to reconstruct the release pipeline to
// get one .nupkg.
func newStackPackCmd() *cobra.Command {
	var out string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "pack [repo]",
		Short: "Build a member's packages here, where the overlay makes siblings resolve",
		Long: "Builds the publishable packages for one member (or every member, with no\n" +
			"argument) from inside the stackspace, so references that cross to a sibling\n" +
			"resolve from that sibling's source through the build overlay.\n\n" +
			"A plain checkout of a proposed branch cannot do this: it has neither the\n" +
			"sibling nor, necessarily, a feed carrying the version the branch pins, and\n" +
			"the restore that fails does not always say why. `rig stack propose` names\n" +
			"the references this applies to when it sends a branch.\n\n" +
			"Refuses while the overlay is missing — packing without it produces exactly\n" +
			"the packages a bare checkout would, which is the thing being avoided.\n" +
			"Output goes to dist/ unless --out says otherwise.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			names, err := stackPackTargets(m, args)
			if err != nil {
				return err
			}
			// Nothing to pack from a member that is not here, and the error
			// naming the import is more use than one naming an empty scan.
			if missing := stackMissingPrefixes(repo.Dir, names); len(missing) > 0 {
				return fmt.Errorf("%s not imported yet — run `rig stack setup` before packing", strings.Join(missing, ", "))
			}
			if err := stackPackRequireOverlay(ctx, repo.Dir, m); err != nil {
				return err
			}
			dir := out
			if dir == "" {
				dir = filepath.Join(repo.Dir, stackPackDefaultOut)
			} else if !filepath.IsAbs(dir) {
				// Relative to where the user is standing, not to the stackspace
				// root — they typed it, and a path that silently means somewhere
				// else is the bug this whole command exists downstream of.
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				dir = abs
			}
			return stackPack(ctx, cmd.OutOrStdout(), repo.Dir, names, dir, dryRun)
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "directory to place the built packages in (default dist/ at the stackspace root)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "say what would be built, and build nothing")
	return cmd
}

// stackPackTargets resolves the argument to the members to pack: the one named,
// or every member when none was.
func stackPackTargets(m *stackManifest, args []string) ([]string, error) {
	if len(args) == 0 {
		if err := m.requireRepos(); err != nil {
			return nil, err
		}
		return m.names(), nil
	}
	name := args[0]
	if m.Repos[name] == nil {
		return nil, fmt.Errorf("no stack repo %q (have: %s)", name, strings.Join(m.names(), ", "))
	}
	return []string{name}, nil
}

// stackPackRequireOverlay refuses to pack while the overlay is not in effect.
//
// Without it every cross-member reference resolves from a registry, so the
// packages this produces are the ones a bare checkout produces — which is the
// situation the command exists to replace. Better to say so than to hand back
// artifacts that are wrong in a way nothing downstream can detect.
//
// A scan that failed is not an answer either way, and is reported as itself
// rather than as a missing overlay.
func stackPackRequireOverlay(ctx context.Context, root string, m *stackManifest) error {
	reports, _, _, failed := stackCheckOverlay(ctx, root, m)
	if len(failed) > 0 {
		ids := make([]string, 0, len(failed))
		for id := range failed {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return fmt.Errorf("could not check the build overlay (%s) — packing without knowing it is in effect would produce packages that resolve siblings from a registry", strings.Join(ids, ", "))
	}
	for _, rep := range reports {
		// Only a report with links matters: an ecosystem where nothing crosses
		// needs no overlay, and its "left over" finding is about tidiness, not
		// about whether this build will resolve correctly.
		if len(rep.Links) == 0 {
			continue
		}
		if len(rep.Resp.Problems) > 0 {
			return fmt.Errorf("the %s build overlay is not in effect, so these packages would resolve their siblings from a registry — which is the thing packing here avoids\n%s",
				rep.Eco, rep.Resp.Problems[0].Message)
		}
	}
	return nil
}

// stackPack builds each member's packages into dir.
func stackPack(ctx context.Context, out io.Writer, root string, names []string, dir string, dryRun bool) error {
	if !dryRun {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var packed int
	for _, name := range names {
		pkgs, err := stackPackPackages(ctx, root, name)
		if err != nil {
			return err
		}
		if len(pkgs) == 0 {
			fmt.Fprintf(out, "%s: nothing publishable to pack\n", name)
			continue
		}
		for _, p := range pkgs {
			// What was in the output directory before this package built, so the
			// listing afterwards can be what actually appeared.
			before := stackPackDirState(dir)
			resp, err := p.eco.Artifacts(ctx, plugin.ArtifactsRequest{
				RepoRoot: root, Package: p.pkg, OutputDir: dir, DryRun: dryRun,
			})
			if err != nil {
				return fmt.Errorf("packing %s (%s): %w", p.pkg.Name, name, err)
			}
			switch {
			case dryRun:
				fmt.Fprintf(out, "%s: would pack %s\n", name, p.pkg.Name)
			case resp.Skipped || !resp.Built:
				fmt.Fprintf(out, "%s: skipped %s%s\n", name, p.pkg.Name, stackPackWhy(resp.Message))
			default:
				packed++
				fmt.Fprintf(out, "%s: packed %s\n", name, p.pkg.Name)
				for _, f := range stackPackNewFiles(dir, before) {
					fmt.Fprintf(out, "    %s\n", f)
				}
			}
		}
	}
	if packed > 0 {
		fmt.Fprintf(out, "%d package(s) in %s\n", packed, dir)
	}
	return nil
}

// stackPackWhy attaches an adapter's reason for skipping, when it gave one.
func stackPackWhy(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return ""
	}
	return " — " + msg
}

// stackPackDirState lists the output directory, for diffing against it after a
// build. An unreadable directory answers empty, which makes the listing after
// the build empty too — the pack still happened and is still reported.
func stackPackDirState(dir string) map[string]bool {
	seen := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return seen
	}
	for _, e := range entries {
		seen[e.Name()] = true
	}
	return seen
}

// stackPackNewFiles names what appeared in dir since before was taken.
//
// Observed rather than taken from the adapter's response, because that response
// is a prediction: the .NET adapter names the file <PackageId>.<Version>.nupkg
// from the version it was handed, and a project that computes its version at
// build time (MinVer, and anything like it) is discovered with none — so the
// predicted name has an empty version in it and matches no file on disk.
// Printing a path that does not exist is worse than printing nothing.
func stackPackNewFiles(dir string, before map[string]bool) []string {
	var out []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() && !before[e.Name()] {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// stackPackPackage pairs a discovered package with the ecosystem that found it.
type stackPackPackage struct {
	eco plugin.Ecosystem
	pkg plugin.Package
}

// stackPackPackages finds the publishable packages under one member.
//
// Discovery runs over the whole stackspace and is filtered by directory
// afterwards, rather than being pointed at the member: an adapter reads shared
// files above the member — Directory.Build.props, a workspace manifest — and
// scanning the subtree alone would miss the version they carry.
//
// Private packages are dropped. They are versioned but never published, so
// packing one produces something with nowhere to go.
func stackPackPackages(ctx context.Context, root, name string) ([]stackPackPackage, error) {
	var out []stackPackPackage
	for _, eco := range ecosystem.Default().All() {
		if len(eco.Info().Overlays) > 0 {
			continue // re-emits the base language's projects; asking both double-counts
		}
		ok, err := eco.Detect(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("scanning for %s projects: %w", eco.Info().ID, err)
		}
		if !ok {
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
		if err != nil {
			return nil, fmt.Errorf("discovering %s packages: %w", eco.Info().ID, err)
		}
		for _, pkg := range resp.Packages {
			if pkg.Private || !stackPackUnder(name, pkg.Dir) {
				continue
			}
			out = append(out, stackPackPackage{eco: eco, pkg: pkg})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pkg.Name < out[j].pkg.Name })
	return out, nil
}

// stackPackUnder reports whether a package directory belongs to the member.
func stackPackUnder(name, dir string) bool {
	dir = filepath.ToSlash(dir)
	return dir == name || strings.HasPrefix(dir, name+"/")
}
