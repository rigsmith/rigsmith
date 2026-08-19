package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/spf13/cobra"
)

// sharedRoot is the tree profiles link their session history into, plus whether
// `clauderig sync` will actually back it up.
type sharedRoot struct {
	Path string
	// SyncedBy is the root id covering Path in the SAVED config, or "" when
	// nothing does. Sharing still works then — it just is not a backup, and
	// saying so would be a lie.
	SyncedBy string
}

// Backed reports whether the shared tree is covered by an enabled sync root.
func (s sharedRoot) Backed() bool { return s.SyncedBy != "" }

// resolveSharedRoot finds the Desktop application-support directory and decides
// whether sync covers it.
//
// Resolved from the SAVED configuration rather than the compiled-in defaults:
// `clauderig init` can persist the Desktop root disabled, and sync skips
// disabled roots — so reading the defaults would let `share` promise a backup
// that will never happen. The location itself still falls back to the default
// when no config names it, because the directory exists regardless of whether
// clauderig has been told to sync it.
func resolveSharedRoot() (sharedRoot, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return sharedRoot{}, err
	}
	me := config.Detect(machineName(cfg))
	for _, r := range cfg.Roots {
		if r.ID != desktopRootID {
			continue
		}
		loc, st := cfg.RootLocation(r.ID, me)
		if st != pathmap.StatusResolved || loc == "" {
			break
		}
		out := sharedRoot{Path: loc}
		if r.Enabled {
			out.SyncedBy = r.ID
		}
		return out, nil
	}
	// No usable Desktop root in the config: fall back to the platform default so
	// sharing still works, but claim nothing about backup.
	m := config.Detect("")
	for _, r := range config.DefaultRoots() {
		if r.ID != desktopRootID {
			continue
		}
		res := m.Resolver().Resolve(r.Location.RawFor(m.OS))
		if res.Path == "" {
			break
		}
		return sharedRoot{Path: res.Path}, nil
	}
	return sharedRoot{}, errors.New("could not resolve the Claude Desktop application-support directory")
}

// desktopRootID is the sync root covering Desktop's application-support tree.
const desktopRootID = "desktop"

// sharedDirsFor is the set of session trees to act on.
func sharedDirsFor(includeCowork bool) []string {
	dirs := append([]string{}, desktop.SharedDirs...)
	if includeCowork {
		dirs = append(dirs, desktop.CoworkDir)
	}
	return dirs
}

func newDesktopShareCmd() *cobra.Command {
	var cowork, all bool
	cmd := &cobra.Command{
		Use:   "share [<name|email>]",
		Short: "Share Claude Code session history between Desktop profiles",
		Long: "Points a profile's session directory at one shared tree, so a Claude Code\n" +
			"session started in any profile's window appears in all of them — and, because\n" +
			"the shared tree is the default Desktop root that `clauderig sync` already\n" +
			"watches, profile history starts being backed up too.\n\n" +
			"WHEN TO RUN IT: after the profiles exist and you have logged into each, with\n" +
			"every Claude Desktop window CLOSED. Typically once, right after setting the\n" +
			"profiles up:\n\n" +
			"    clauderig desktop add work        # log in, then close the window\n" +
			"    clauderig desktop add personal    # log in, then close the window\n" +
			"    clauderig desktop share --all\n\n" +
			"Run it again whenever you add a profile — it is idempotent, so re-running it\n" +
			"only links whatever is not linked yet.\n\n" +
			"Safe by construction: these trees are partitioned by account uuid, so two\n" +
			"profiles signed into different accounts write to different subdirectories.\n" +
			"Existing history is migrated into the shared tree first, and migration never\n" +
			"overwrites a file that is already there.\n\n" +
			"The profile's window must be CLOSED — Electron keeps writing through a\n" +
			"directory handle it opened before the swap, so a live relink would silently\n" +
			"lose whatever it writes next. `--all` shares every saved profile.\n\n" +
			"Opt-in and reversible: `desktop unshare` puts a profile back on its own\n" +
			"directory, leaving the shared history untouched.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShare(cmd, args, cowork, all, true)
		},
	}
	cmd.Flags().BoolVar(&cowork, "cowork", false,
		"also share "+desktop.CoworkDir+" (Cowork history — much larger, holds sandbox working directories)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "apply to every saved profile")
	return cmd
}

func newDesktopUnshareCmd() *cobra.Command {
	var cowork, all bool
	cmd := &cobra.Command{
		Use:   "unshare [<name|email>]",
		Short: "Put a profile back on its own session history",
		Long: "Replaces the shared link with the profile's own (empty) directory.\n\n" +
			"Deliberately non-destructive: the shared history stays where it is. Working\n" +
			"out which sessions 'belong' to this profile and copying them back would be\n" +
			"guesswork, and getting it wrong would either duplicate or delete history.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShare(cmd, args, cowork, all, false)
		},
	}
	cmd.Flags().BoolVar(&cowork, "cowork", false, "also unshare "+desktop.CoworkDir)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "apply to every saved profile")
	return cmd
}

func runShare(cmd *cobra.Command, args []string, cowork, all, on bool) error {
	out := cmd.OutOrStdout()
	app := desktop.New()
	if _, ok := app.Installed(); !ok {
		return desktopUnavailable()
	}
	st, err := desktopStore()
	if err != nil {
		return err
	}
	root, err := resolveSharedRoot()
	if err != nil {
		return err
	}
	dirs := sharedDirsFor(cowork)

	targets, err := shareTargets(st, args, all)
	if err != nil {
		return err
	}

	// The shared tree belongs to the default Desktop profile, which may be
	// running and writing into it. Migration only ever adds files, so that is
	// safe — but repointing a profile's own directory is not, so each profile is
	// checked below.
	for _, p := range targets {
		open, rerr := desktop.IsRunning(app, p.DataDir())
		if rerr != nil {
			return fmt.Errorf("could not tell whether %s is open: %w\n"+
				"Refusing to move its session history on a guess", p.Name, rerr)
		}
		if open {
			return fmt.Errorf("%w: %s\n"+
				"Close it first (`clauderig desktop quit %s`) — Claude Desktop keeps writing "+
				"through a directory it opened before the swap, so relinking a live profile "+
				"would silently lose whatever it writes next",
				desktop.ErrProfileOpen, p.Name, p.Name)
		}
	}

	for _, p := range targets {
		if !on {
			if uerr := desktop.Unshare(p, root.Path, dirs); uerr != nil {
				return uerr
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ unshared"), p.Label())
			continue
		}
		results, serr := desktop.Share(p, root.Path, dirs)
		if serr != nil {
			return serr
		}
		fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ sharing"), p.Label())
		for _, r := range results {
			switch {
			case r.Migrated == 0 && r.Skipped == 0 && r.Conflicts == 0:
				fmt.Fprintf(out, "%s\n", DimStyle.Render("    "+r.Dir+" → shared"))
			default:
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
					"    %s → shared (%d migrated, %d already there)", r.Dir, r.Migrated, r.Skipped)))
			}
			// Preserved files nobody is told about are barely better than lost
			// ones, so a conflict is reported rather than counted quietly.
			if r.Conflicts > 0 {
				fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
					"    %d file(s) differed from the shared copy — the shared version was kept", r.Conflicts)))
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"      this profile's versions preserved at "+r.ConflictDir))
			}
		}
	}
	if on {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("shared tree: "+root.Path))
		if root.Backed() {
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"`clauderig sync` already covers it, so this history is backed up now too"))
		} else {
			// Never claim a backup that will not happen: the Desktop root is
			// absent or disabled in the saved config, and sync skips it.
			fmt.Fprintf(out, "%s\n", WarnStyle.Render(
				"note: the desktop sync root is disabled, so this history is NOT backed up"))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  enable it in `clauderig config` (or re-run `clauderig init`) to include it"))
		}
	} else {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("the shared history is untouched — nothing was deleted"))
	}
	return nil
}

// shareTargets resolves which profiles a share/unshare applies to.
func shareTargets(st *desktop.Store, args []string, all bool) ([]desktop.Profile, error) {
	if all {
		if len(args) > 0 {
			return nil, errors.New("give a profile name or --all, not both")
		}
		profiles, err := st.List()
		if err != nil {
			return nil, err
		}
		if len(profiles) == 0 {
			return nil, errors.New("no Desktop profiles yet — `clauderig desktop add <name>` creates one")
		}
		return profiles, nil
	}
	ref, err := desktopRefFor(args)
	if err != nil {
		return nil, err
	}
	p, err := st.Resolve(ref)
	if err != nil {
		return nil, desktopNotFound(err, ref)
	}
	return []desktop.Profile{p}, nil
}

// profileShareState is used by `list` and the TUI to show whether a profile is
// sharing. Resolved once per listing, not per row, since the root is fixed.
func profileShareState(p desktop.Profile, root string) bool {
	if root == "" {
		return false
	}
	return desktop.ShareStatus(p, root, desktop.SharedDirs).Shared(desktop.SharedDirs)
}

// sharedRootOrEmpty resolves the shared tree's path for listings, where a
// failure should degrade to "not sharing" rather than break the output.
func sharedRootOrEmpty() string {
	root, err := resolveSharedRoot()
	if err != nil {
		return ""
	}
	if _, serr := os.Stat(filepath.Dir(root.Path)); serr != nil {
		return ""
	}
	return root.Path
}
