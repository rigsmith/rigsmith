package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/changelog"
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
	}
	cmd.AddCommand(newChangelogAddCmd())
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

// changelogEntry renders the prepended block: a `## <version|Unreleased>` heading
// followed by a single bullet (optionally prefixed by a bold type label).
func changelogEntry(version, typ, message string) string {
	heading := strings.TrimSpace(version)
	if heading == "" {
		heading = "Unreleased"
	}
	bullet := "- " + strings.TrimSpace(message)
	if t := strings.TrimSpace(typ); t != "" {
		bullet = "- **" + t + ":** " + strings.TrimSpace(message)
	}
	return fmt.Sprintf("## %s\n\n%s\n", heading, bullet)
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
