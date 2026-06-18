package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/changelog"
	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/mdfmt"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// NewChangelogCmd is the `changelog` command group: manual changelog edits that
// sit outside the changeset → version flow (the escape hatch for backfilled
// history, corrections, or notes the generator can't produce).
func NewChangelogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Manually edit a package's CHANGELOG.md (outside the changeset flow)",
		// Bare `changelog` on a TTY opens the subcommand menu; with a verb or off a
		// TTY the subcommands stand (and `changelog -h` still prints help).
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newChangelogAddCmd(), newChangelogFormatCmd())
	return cmd
}

func newChangelogAddCmd() *cobra.Command {
	var (
		message string
		typ     string
		version string
	)
	cmd := &cobra.Command{
		Use:   "add [package]",
		Short: "Prepend a hand-authored entry to a package's CHANGELOG.md",
		Long: "Prepend a hand-authored entry to a package's CHANGELOG.md, right now —\n" +
			"outside the changeset → version cycle. For backfilling pre-tool history,\n" +
			"corrections, or notes the generator can't produce.\n\n" +
			"The entry goes under a `## <version>` heading (default: a new \"Unreleased\"\n" +
			"section at the top). In a single-package repo the package arg is optional.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(message) == "" {
				return fmt.Errorf("a changelog entry is required (-m/--message)")
			}
			ws, err := Open()
			if err != nil {
				return err
			}
			pkgs, _, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			pkg, err := resolveChangelogPackage(pkgs, args)
			if err != nil {
				return err
			}

			entry := changelogEntry(version, typ, message)
			dir := filepath.Join(ws.Root, pkg.Dir)
			if err := changelog.WriteEntry(dir, displayName(pkg), entry); err != nil {
				return fmt.Errorf("writing changelog: %w", err)
			}
			// Keep the file tidy after the prepend — same formatting the `version`
			// step applies to released entries.
			formatChangelogFile(cmd, ws, filepath.Join(dir, changelog.FileName))
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n",
				PatchStyle.Render("changelog +"),
				filepath.Join(pkg.Dir, changelog.FileName))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&message, "message", "m", "", "the changelog entry text (required)")
	f.StringVarP(&typ, "type", "t", "", "optional label shown before the entry (e.g. feat, fix)")
	f.StringVar(&version, "version", "", "release heading to file under (default: a new \"Unreleased\" section)")
	return cmd
}

func newChangelogFormatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "format [package]",
		Short: "Reformat a package's CHANGELOG.md (e.g. after a hand-edit)",
		Long: "Reformat a package's CHANGELOG.md in place. Uses the configured `format`\n" +
			"formatter (the same one the version step applies to released entries), or\n" +
			"the built-in native markdown formatter when none is configured.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			pkgs, _, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			pkg, err := resolveChangelogPackage(pkgs, args)
			if err != nil {
				return err
			}
			rel := filepath.Join(pkg.Dir, changelog.FileName)
			path := filepath.Join(ws.Root, rel)
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no %s for %s", changelog.FileName, pkg.Name)
			}
			formatChangelogFile(cmd, ws, path)
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", DimStyle.Render("formatted"), rel)
			return nil
		},
	}
	return cmd
}

// formatChangelogFile rewrites path through the configured `format` formatter
// (matching how the version step formats released changelogs), falling back to
// the dependency-free native markdown formatter when none is configured — so a
// manual edit stays tidy regardless of config. Formatting failures only warn.
func formatChangelogFile(cmd *cobra.Command, ws *Workspace, path string) {
	warnf := func(format string, a ...any) {
		fmt.Fprintln(cmd.ErrOrStderr(), DimStyle.Render("warn "+fmt.Sprintf(format, a...)))
	}
	if argv, ok := ws.Config.FormatCommand(); ok {
		mdfmt.FormatFilesCustom([]string{path}, argv, ws.Root, mdfmt.Runner(execRunner(cmd)), warnf)
		return
	}
	if spec := ws.Config.FormatSpec(); spec != "" {
		mdfmt.FormatFiles([]string{path}, spec, ws.Root, mdfmt.Runner(execRunner(cmd)), warnf)
		return
	}
	// No formatter configured — apply the native one so the file still gets tidied.
	data, err := os.ReadFile(path)
	if err != nil {
		warnf("read %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, []byte(mdfmt.Format(string(data))), 0o644); err != nil {
		warnf("write %s: %v", path, err)
	}
}

// changelogEntry renders the prepended block: a `## <version|Unreleased>` heading
// followed by a single bullet (optionally prefixed by a bold type label). Each
// field is flattened to a single line so a multi-line input can't inject extra
// headings/bullets and corrupt the changelog structure.
func changelogEntry(version, typ, message string) string {
	heading := singleLine(version)
	if heading == "" {
		heading = "Unreleased"
	}
	msg := singleLine(message)
	bullet := "- " + msg
	if t := singleLine(typ); t != "" {
		bullet = "- **" + t + ":** " + msg
	}
	return fmt.Sprintf("## %s\n\n%s\n", heading, bullet)
}

// singleLine collapses all runs of whitespace (including newlines) to single
// spaces and trims — so a field stays one line.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// resolveChangelogPackage picks the target package: the named one, or the sole
// discovered package when no name is given.
func resolveChangelogPackage(pkgs []plugin.Package, args []string) (plugin.Package, error) {
	if len(args) == 1 {
		for _, p := range pkgs {
			if p.Name == args[0] {
				return p, nil
			}
		}
		return plugin.Package{}, fmt.Errorf("package %q not found", args[0])
	}
	switch len(pkgs) {
	case 0:
		return plugin.Package{}, fmt.Errorf("no packages discovered")
	case 1:
		return pkgs[0], nil
	default:
		names := make([]string, len(pkgs))
		for i, p := range pkgs {
			names[i] = p.Name
		}
		sort.Strings(names) // deterministic message regardless of discovery order
		return plugin.Package{}, fmt.Errorf("multiple packages — name one of: %s", strings.Join(names, ", "))
	}
}

// displayName is a package's changelog title: DisplayName, falling back to Name.
func displayName(pkg plugin.Package) string {
	if pkg.DisplayName != "" {
		return pkg.DisplayName
	}
	return pkg.Name
}
