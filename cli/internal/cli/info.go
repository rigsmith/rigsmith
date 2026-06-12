package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/rigsmith/core/ecosystem"
	"github.com/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

var headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show what rig discovered for this repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)
			out := cmd.OutOrStdout()

			cfg, _ := config.Load(root)

			fmt.Fprintln(out, headerStyle.Render("Repo"))
			fmt.Fprintf(out, "  root:    %s\n", root)
			// primary is the resolved ecosystem; resolved is what we actually use
			// for the dev verbs (id, or "" when none/ambiguous).
			primary, resolved := primaryDisplay(cwd, root, cfg)
			fmt.Fprintf(out, "  primary: %s\n\n", primary)

			fmt.Fprintln(out, headerStyle.Render("Config"))
			if cfg.Path == "" {
				fmt.Fprintln(out, dimStyle.Render("  (no .rig.json)"))
			} else {
				fmt.Fprintf(out, "  file:           %s\n", cfg.Path)
				fmt.Fprintf(out, "  defaultProject: %s\n", orNone(cfg.DefaultProject))
				fmt.Fprintf(out, "  ecosystem:      %s\n", orNone(cfg.Ecosystem))
				fmt.Fprintf(out, "  quiet:          %t\n", cfg.IsQuiet())
				if cfg.Coverage != nil {
					fmt.Fprintf(out, "  coverage:       %s\n", coverageDefaults(cfg.Coverage))
				}
				if len(cfg.Commands) > 0 {
					names := make([]string, 0, len(cfg.Commands))
					for name := range cfg.Commands {
						names = append(names, name)
					}
					sort.Strings(names)
					fmt.Fprintf(out, "  commands:       %v\n", names)
				}
			}
			fmt.Fprintln(out)

			// Verbs the resolved ecosystem maps (dev loop + maintenance).
			if resolved != "" {
				fmt.Fprintln(out, headerStyle.Render("Commands"))
				for _, verb := range []string{
					plugin.VerbBuild, plugin.VerbTest, plugin.VerbRun,
					plugin.VerbFormat, plugin.VerbLint, plugin.VerbTypecheck,
					plugin.VerbInstall, plugin.VerbAdd, plugin.VerbUninstall,
					plugin.VerbOutdated, plugin.VerbUpgrade, plugin.VerbClean,
				} {
					if argv, ok := detect.CommandFor(resolved, verb, root); ok {
						fmt.Fprintf(out, "  %-10s %s\n", verb, dimStyle.Render(strings.Join(argv, " ")))
					} else {
						fmt.Fprintf(out, "  %-10s %s\n", verb, dimStyle.Render("(no mapping)"))
					}
				}
				fmt.Fprintln(out)
			}

			reg := ecosystem.Default()
			fmt.Fprintln(out, headerStyle.Render("Projects"))
			var all []plugin.Package
			for _, eco := range reg.All() {
				if ok, _ := eco.Detect(cmd.Context(), root); !ok {
					continue
				}
				resp, err := eco.Discover(cmd.Context(), plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
				if err != nil {
					continue
				}
				all = append(all, resp.Packages...)
			}
			sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
			shown := 0
			for _, p := range all {
				if excluded(p.Name, cfg.Exclude) {
					continue
				}
				fmt.Fprintf(out, "  %s %s\n", p.Name, dimStyle.Render(p.Version))
				shown++
			}
			if shown == 0 {
				fmt.Fprintln(out, dimStyle.Render("  (none discovered)"))
			}
			return nil
		},
	}
}

// primaryDisplay resolves the primary ecosystem for the info view. It returns a
// human display string and the resolved ecosystem id ("" when none/ambiguous so
// the Dev commands section is skipped). .rig.json's "ecosystem" wins; otherwise
// the nearest manifest decides, and a tie is shown as ambiguous rather than
// guessed.
func primaryDisplay(cwd, root string, cfg config.Config) (display, resolved string) {
	if cfg.Ecosystem != "" {
		return cfg.Ecosystem + dimStyle.Render(" (from .rig.json)"), cfg.Ecosystem
	}
	id, candidates := detect.NearestEcosystem(cwd)
	if len(candidates) > 0 {
		return fmt.Sprintf("ambiguous: %s — set ecosystem in %s",
			strings.Join(candidates, ", "), config.FileName), ""
	}
	return orNone(id), id
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// coverageDefaults summarizes the persisted coverage prefs actually in effect
// (so a repo that gates at N% isn't a surprise), or "(none)". The license is
// deliberately excluded — it isn't a default that changes a run's outcome.
// Mirrors the .NET rig's InfoVerb.CoverageDefaults. Pure.
func coverageDefaults(c *config.Coverage) string {
	if c == nil {
		return "(none)"
	}
	var parts []string
	if c.Min != nil {
		parts = append(parts, "min "+trimFloat(*c.Min)+"%")
	}
	if c.Open != nil && *c.Open {
		parts = append(parts, "auto-open")
	}
	if c.Full != nil && *c.Full {
		parts = append(parts, "full report")
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}
