package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

var headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show what rig discovered for this repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			out := cmd.OutOrStdout()

			cfg, _ := config.LoadMerged(root)

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

			// Anything wrong with that config, inside the report about it. `info`
			// is where you look when rig isn't doing what the config says, so the
			// answer belongs here too, not only on the per-run stderr notice.
			printConfigProblems(out, cfg, "Warnings")

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

			fmt.Fprintln(out, headerStyle.Render("Projects"))
			all := discoverWorkspace(cmd.Context(), root, cfg.Exclude)
			sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
			// A name shared by several paths (a duplicate — usually a second
			// checkout of the same project) is indistinguishable by name alone,
			// so show its path to tell them apart. That path column used to be
			// the only signal, which reads as ragged formatting rather than a
			// warning — so duplicates are now summarized up front, labelled as
			// duplicates, and their rows are marked.
			dups := duplicateNames(all)
			warnings := projectWarnings(cdContext(cmd), root, cfg, all, dups)
			for _, w := range warnings {
				fmt.Fprintln(out, warnStyle.Render("  ⚠  ")+w)
			}
			if len(warnings) > 0 {
				fmt.Fprintln(out)
			}
			for _, p := range all {
				gutter, meta := "   ", p.Version
				if dups[p.Name] {
					gutter = warnStyle.Render(" ⚠ ")
					if rel := relSlash(root, p.Dir); rel != "" && rel != "." {
						if meta != "" {
							meta += "  "
						}
						meta += rel
					}
				}
				fmt.Fprintf(out, " %s%s %s\n", gutter, p.Name, dimStyle.Render(meta))
			}
			if len(all) == 0 {
				fmt.Fprintln(out, dimStyle.Render("  (none discovered)"))
			}
			return nil
		},
	}
}

// projectWarnings builds the lines `info` leads the Projects section with: the
// duplicate names discovery found (flagged when one of them is the configured
// defaultProject, since that is the case where a bare `rig run` has two answers)
// and the nested git worktrees whose copies discovery is holding back.
//
// The worktree line closes the loop the tools otherwise leave open: `rig wt`
// creates the checkout, `rig prune` knows when it is merged and removable, but
// nothing ever said the two were related. Here they are, in one place, at the
// moment the extra copies would matter.
func projectWarnings(ctx context.Context, root string, cfg config.Config, all []target, dups map[string]bool) []string {
	names := make([]string, 0, len(all))
	for _, p := range all {
		names = append(names, p.Name)
	}
	// Which names the default resolves to, by the same rules `rig run` applies —
	// so the label lands on the name that would actually launch.
	isDefault := topTierNames(names, cfg.DefaultProject)

	var out []string
	for _, name := range sortedKeys(dups) {
		var paths []string
		for _, p := range all {
			if p.Name == name {
				paths = append(paths, relSlash(root, p.Dir))
			}
		}
		label := fmt.Sprintf("%d projects named %s", len(paths), name)
		if isDefault[name] {
			label += " (defaultProject)"
		}
		out = append(out, label+" — "+strings.Join(paths, ", "))
	}

	// Nested worktrees hold a full copy of every project; discovery skips them
	// (see nestedwt.go) unless --include-worktrees says otherwise.
	for _, w := range nestedWorktrees(ctx, root) {
		what := "nested worktree " + w.Rel
		if w.Branch != "" {
			what += " (" + w.Branch + ")"
		}
		if includeWorktrees {
			what += " is included in the list above (--include-worktrees)"
		} else {
			what += " is hidden from discovery"
		}
		// w.State is prune's own verdict, so the line never promises a removal
		// prune would decline. It's the more actionable half, so it takes the
		// tail — the --include-worktrees hint only shows when there's no verdict.
		switch {
		case w.State != "":
			what += "; it " + w.State
		case !includeWorktrees:
			what += " — show it with --include-worktrees"
		}
		out = append(out, what)
	}
	return out
}

// sortedKeys returns a name set's members in stable order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
