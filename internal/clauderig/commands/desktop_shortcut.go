package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/spf13/cobra"
)

func newDesktopShortcutCmd() *cobra.Command {
	var to []string
	var label, exe string
	var all, remove, force bool

	cmd := &cobra.Command{
		Use:   "shortcut [<name|email>]",
		Short: "Make a clickable launcher for a profile (desktop icon, or app menu entry)",
		Long: "Writes a launcher that opens one profile's Claude Desktop window: a small\n" +
			"application bundle on macOS, a .lnk on Windows.\n\n" +
			"The shortcut runs `clauderig desktop open <name>`, not Claude directly, so\n" +
			"clicking it twice FOCUSES the window instead of starting a second one on the\n" +
			"same profile — and a profile that has never been opened still gets set up\n" +
			"properly on its first click.\n\n" +
			"  --to desktop   the desktop itself (the default)\n" +
			"  --to apps      ~/Applications on macOS (so Spotlight and Launchpad find\n" +
			"                 it), the Start Menu on Windows\n\n" +
			"Repeat --to for both. Re-running rewrites the shortcuts it already made,\n" +
			"which is also how you repair them after moving the clauderig binary. `--rm`\n" +
			"deletes them; `clauderig desktop rm` deletes them with the profile.\n\n" +
			"The window itself still shows Claude's own icon and name once it is open —\n" +
			"macOS and Windows brand a window by the app that owns it, and that app is\n" +
			"Claude Desktop for every profile.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if !desktop.ShortcutsSupported() {
				return desktopUnavailable()
			}
			st, err := desktopStore()
			if err != nil {
				return err
			}
			app := desktop.New()

			if all && label != "" {
				return errors.New("--label names one shortcut, so it cannot be combined with --all")
			}
			targets, err := shortcutTargets(st, app, args, all)
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}

			if remove {
				return removeShortcuts(out, targets)
			}

			// Only for CREATION: a shortcut that opens an app which is not
			// installed is a broken icon. Removing them stays possible after
			// Claude Desktop has been uninstalled, which is exactly when
			// somebody wants to clear them away.
			if _, ok := app.Installed(); !ok {
				return desktopUnavailable()
			}
			dests, err := parseDests(to)
			if err != nil {
				return err
			}
			binary, err := shortcutExe(exe)
			if err != nil {
				return err
			}
			return writeShortcuts(out, targets, dests, label, binary, force)
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", []string{string(desktop.DestDesktop)},
		"where to put it: desktop, apps (repeatable)")
	cmd.Flags().StringVar(&label, "label", "", "name to show under the icon (default \"Claude - <profile>\")")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "one shortcut per saved profile")
	cmd.Flags().BoolVar(&remove, "rm", false, "delete this profile's shortcuts instead of making them")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace a file of the same name that clauderig did not create")
	cmd.Flags().StringVar(&exe, "exe", "",
		"path to the clauderig binary the shortcut should run (default: this one)")
	return cmd
}

// shortcutTargets resolves which profiles to act on: every one under --all,
// otherwise the single profile the usual way (named, mapped to this directory,
// or picked from a list).
func shortcutTargets(st *desktop.Store, app desktop.App, args []string, all bool) ([]desktop.Profile, error) {
	if !all {
		p, err := resolveDesktopTarget(st, app, args, false)
		if err != nil {
			return nil, err
		}
		return []desktop.Profile{p}, nil
	}
	if len(args) > 0 && args[0] != "" {
		return nil, fmt.Errorf("--all means every profile, so it cannot also name %q", args[0])
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

func parseDests(to []string) ([]desktop.Dest, error) {
	var dests []desktop.Dest
	seen := map[desktop.Dest]bool{}
	for _, raw := range to {
		d, err := desktop.ParseDest(raw)
		if err != nil {
			return nil, err
		}
		if seen[d] {
			continue // `--to desktop --to desktop` is one shortcut, not two writes
		}
		seen[d] = true
		dests = append(dests, d)
	}
	if len(dests) == 0 {
		return nil, errors.New("--to needs a location: desktop or apps")
	}
	return dests, nil
}

// shortcutExe decides which clauderig binary the shortcut will run.
func shortcutExe(override string) (string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", err
		}
		info, serr := os.Stat(abs)
		if serr != nil {
			return "", fmt.Errorf("--exe %s: %w", abs, serr)
		}
		if info.IsDir() {
			return "", fmt.Errorf("--exe %s is a directory — name the clauderig binary itself", abs)
		}
		return abs, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not work out where this clauderig binary lives: %w", err)
	}
	// Deliberately NOT resolved through the symlink. A Homebrew install is
	// /opt/homebrew/bin/clauderig → ../Cellar/clauderig/<version>/…, and pinning
	// the shortcut to the versioned path behind it would break on the next
	// upgrade. The stable name is the one the user invoked.
	if tmp, is := underTempDir(exe); is {
		return "", fmt.Errorf(
			"this clauderig is a temporary build (%s, under %s), and a shortcut to it "+
				"would stop working as soon as that is cleaned up.\n"+
				"Install clauderig first, or point the shortcut at an installed copy with --exe <path>",
			exe, tmp)
	}
	return exe, nil
}

// underTempDir reports whether a path sits in the system temporary directory —
// where `go run` and `go test` leave the binaries they build.
func underTempDir(path string) (string, bool) {
	tmp := resolveLinks(os.TempDir())
	real := resolveLinks(path)
	if real == tmp || strings.HasPrefix(real+string(filepath.Separator), tmp+string(filepath.Separator)) {
		return tmp, true
	}
	return "", false
}

// resolveLinks resolves symlinks as far as the path exists.
//
// Both sides of the comparison have to be resolved the same way or it is
// meaningless: macOS's temp directory is /var/folders/…, and /var is itself a
// symlink to /private/var, so an unresolved path never matches a resolved root.
// The parent is tried when the path itself does not exist, which is the case
// for a shortcut target being validated before anything is written.
func resolveLinks(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(p)
	}
	dir, base := filepath.Split(path)
	if d, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(d, base)
	}
	return filepath.Clean(path)
}

func writeShortcuts(out io.Writer, targets []desktop.Profile, dests []desktop.Dest, label, exe string, force bool) error {
	for _, p := range targets {
		for _, d := range dests {
			sc, err := desktop.InstallShortcut(desktop.ShortcutSpec{
				Profile: p.Name,
				Label:   label,
				Dest:    d,
				Exe:     exe,
				Force:   force,
			})
			if errors.Is(err, desktop.ErrShortcutExists) {
				return fmt.Errorf("%w\nGive it another name with --label, or replace that file with --force", err)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ shortcut"), sc.Path)
		}
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render(
		"  it runs `clauderig desktop open <profile>`, so clicking it again focuses the\n"+
			"  window rather than opening a second one. Moved clauderig? Run this again."))
	return nil
}

func removeShortcuts(out io.Writer, targets []desktop.Profile) error {
	var removed int
	var errs []error
	for _, p := range targets {
		gone, err := desktop.RemoveShortcutsFor(p.Name)
		if err != nil {
			errs = append(errs, err)
		}
		for _, sc := range gone {
			removed++
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ removed"), sc.Path)
		}
	}
	if removed == 0 && len(errs) == 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("no shortcuts to remove"))
	}
	return errors.Join(errs...)
}

// offerShortcut asks, at the end of `desktop add`, whether to put an icon on the
// desktop — the moment the profile exists and the question makes sense.
//
// Never prompts off a terminal, and never fails the command it is attached to:
// the profile has already been created and its window opened by the time this
// runs, so a shortcut that could not be written is a note, not an error.
func offerShortcut(out io.Writer, profile string) {
	if !desktop.ShortcutsSupported() {
		return
	}
	yes, err := askYesNo("Put a shortcut for "+profile+" on your desktop?", true)
	if err != nil || !yes {
		return
	}
	makeShortcut(out, profile)
}

// makeShortcut writes the desktop shortcut for a profile, reporting failure as
// a note. Shared by the `add` prompt and the interactive dashboard path.
func makeShortcut(out io.Writer, profile string) {
	exe, err := shortcutExe("")
	if err != nil {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("  no shortcut: "+err.Error()))
		return
	}
	sc, err := desktop.InstallShortcut(desktop.ShortcutSpec{
		Profile: profile,
		Dest:    desktop.DestDesktop,
		Exe:     exe,
	})
	if err != nil {
		fmt.Fprintf(out, "%s\n", DimStyle.Render(
			"  no shortcut: "+err.Error()+" — `clauderig desktop shortcut "+profile+"` retries"))
		return
	}
	fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ shortcut"), sc.Path)
}

// askYesNo is a plain confirm. Separate from confirmDestructive because this
// question is an offer rather than a warning, and it starts on the answer most
// people want instead of forcing a choice.
func askYesNo(title string, def bool) (bool, error) {
	ok := def
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&ok),
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	return ok, err
}
