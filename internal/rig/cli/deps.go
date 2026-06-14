package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// newDepsCmd builds `rig deps` — a full dependency report: every top-level
// package with its current version, the latest published version, and whether
// an update is available. Unlike `rig outdated` (which lists only packages that
// have an update), `deps` shows up-to-date packages too, so you can always see
// the version details. Rich support: go, .NET, and Node (npm/pnpm/bun/yarn
// classic); other ecosystems fall back to the plain outdated list.
func newDepsCmd() *cobra.Command {
	var updatesOnly, asJSON, vulnerable bool
	cmd := &cobra.Command{
		Use:     "deps",
		Short:   "List dependencies with current and latest versions",
		Aliases: []string{"dependencies"},
		Long: "List every dependency with its current and latest published version.\n\n" +
			"  rig deps              show all dependencies (current → latest)\n" +
			"  rig deps -u           only the ones with an update available\n" +
			"  rig deps --vulnerable add a column with each package's highest known CVE severity\n" +
			"  rig deps --json       machine-readable output",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			if dryRun {
				echo(cmd, fmt.Sprintf("inspect %s dependencies in %s", eco, root))
				return nil
			}

			rows, ok := discoverDeps(cmd, eco, root)
			if !ok {
				fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
					"full dependency report isn't wired for this ecosystem yet — listing updates instead"))
				return runPlainOutdated(cmd, eco, root, nil)
			}
			showVuln := false
			if vulnerable {
				if sev, vok := auditSeverities(cmd, eco, root); vok {
					applyVulnerabilities(rows, sev, eco == detect.DotNet)
					showVuln = true
				} else {
					fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
						"vulnerability scan isn't wired for this ecosystem yet — needs an external scanner"))
				}
			}
			if updatesOnly {
				rows = filterUpdates(rows)
			}
			if asJSON {
				return renderDepsJSON(cmd, rows)
			}
			renderDepsTable(cmd, rows, showVuln)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&updatesOnly, "updates-only", "u", false, "show only dependencies with an update available")
	cmd.Flags().BoolVar(&vulnerable, "vulnerable", false, "add a column with each package's highest known CVE severity")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output the report as JSON")
	return cmd
}

// discoverDeps returns every top-level dependency with current + latest for the
// ecosystem at root. supported=false means the rich report isn't wired for this
// ecosystem (the caller falls back to the plain outdated list).
func discoverDeps(cmd *cobra.Command, eco, root string) (deps []outdatedDep, supported bool) {
	switch eco {
	case detect.Go:
		// `go list -m -u -json all` carries both the current version and any
		// available update per module, so one call gives the full report.
		out, err := captureOutdated(cmd, root, "go", "list", "-m", "-u", "-json", "all")
		if err != nil && out == "" {
			return nil, false
		}
		return parseGoListAll(out), true

	case detect.DotNet:
		out, err := captureOutdated(cmd, root, "dotnet", "list", "package", "--format", "json")
		if err != nil && out == "" {
			return nil, false
		}
		all := parseDotnetList(out)
		if all == nil && strings.TrimSpace(out) != "" && !strings.HasPrefix(strings.TrimSpace(out), "{") {
			return nil, false // SDK too old for --format json
		}
		outdated, _ := discoverOutdated(cmd, eco, root)
		return mergeLatest(all, outdated, true), true

	case detect.Node:
		all, ok := listNodeDeps(cmd, root)
		if !ok {
			return nil, false
		}
		outdated, _ := discoverOutdated(cmd, eco, root)
		return mergeLatest(all, outdated, false), true

	default:
		return nil, false
	}
}

// listNodeDeps lists every top-level Node dependency (with current version) for
// the project's package manager. ok=false for managers without a machine-
// readable list (yarn berry), so the caller falls back.
func listNodeDeps(cmd *cobra.Command, root string) (deps []outdatedDep, ok bool) {
	switch pm := detect.DetectNodePM(root); pm {
	case detect.NPM:
		out, _ := captureOutdated(cmd, root, "npm", "ls", "--json", "--depth=0")
		return parseNpmList(out), true
	case detect.PNPM:
		out, _ := captureOutdated(cmd, root, "pnpm", "ls", "--json", "--depth=0")
		return parsePnpmList(out), true
	case detect.Bun:
		out, _ := captureOutdated(cmd, root, "bun", "pm", "ls")
		return parseBunList(out), true
	case detect.Yarn:
		if yarnIsBerry(cmd, root) {
			return nil, false // berry has no machine-readable list
		}
		out, _ := captureOutdated(cmd, root, "yarn", "list", "--depth=0", "--json")
		return parseYarnClassicList(out), true
	default:
		return nil, false
	}
}

// filterUpdates keeps only deps whose latest differs from current. Pure.
func filterUpdates(deps []outdatedDep) []outdatedDep {
	var out []outdatedDep
	for _, d := range deps {
		if d.hasUpdate() {
			out = append(out, d)
		}
	}
	return out
}

// hasUpdate reports whether a known latest version is newer-than/different-from
// the current one (latest == current means up to date; empty latest is unknown).
func (d outdatedDep) hasUpdate() bool {
	return d.latest != "" && d.latest != d.current
}

var (
	depMarkStyle = lipgloss.NewStyle().Foreground(brandYellow)
	sevHighStyle = lipgloss.NewStyle().Foreground(brandRed)
	sevModStyle  = lipgloss.NewStyle().Foreground(brandYellow)
)

// severityStyle renders a severity label colored by its rank: critical/high in
// red, moderate in yellow, anything lower dim.
func severityStyle(sev string) string {
	switch {
	case severityRank(sev) >= 3: // critical, high
		return sevHighStyle.Render(sev)
	case severityRank(sev) == 2: // moderate
		return sevModStyle.Render(sev)
	default:
		return dimStyle.Render(sev)
	}
}

// renderDepsTable prints the dependency report as an aligned table: a ►-marked
// row per package with an update, the count summary at the end. With showVuln a
// Vuln column carries each package's highest known severity.
func renderDepsTable(cmd *cobra.Command, deps []outdatedDep, showVuln bool) {
	out := cmd.OutOrStdout()
	if len(deps) == 0 {
		fmt.Fprintln(out, dimStyle.Render("no dependencies found"))
		return
	}
	nameW, curW, latW, vulnW := len("Package"), len("Current"), len("Latest"), len("Vuln")
	for _, d := range deps {
		nameW = max(nameW, len(d.name))
		curW = max(curW, len(d.current))
		latW = max(latW, len(d.latest))
		vulnW = max(vulnW, len(d.vuln))
	}

	header := fmt.Sprintf("  %-*s  %-*s  %-*s", nameW, "Package", curW, "Current", latW, "Latest")
	if showVuln {
		header += fmt.Sprintf("  %-*s", vulnW, "Vuln")
	}
	header += "  Status"
	fmt.Fprintln(out, dimStyle.Render(header))

	updates, vulns := 0, 0
	lastProject := ""
	for _, d := range deps {
		if d.project != "" && d.project != lastProject {
			fmt.Fprintln(out, dimStyle.Render("  "+filepath.Base(d.project)))
			lastProject = d.project
		}
		mark, status := " ", dimStyle.Render("up to date")
		if d.hasUpdate() {
			mark, status = depMarkStyle.Render("►"), depMarkStyle.Render("update")
			updates++
		}
		fmt.Fprintf(out, "%s %-*s  %-*s  %-*s", mark, nameW, d.name, curW, d.current, latW, d.latest)
		if showVuln {
			// Pad on the raw width, then colorize, so ANSI codes don't skew columns.
			cell := fmt.Sprintf("%-*s", vulnW, d.vuln)
			if d.vuln != "" {
				cell = severityStyle(d.vuln) + strings.Repeat(" ", vulnW-len(d.vuln))
				vulns++
			}
			fmt.Fprintf(out, "  %s", cell)
		}
		fmt.Fprintf(out, "  %s\n", status)
	}

	summary := fmt.Sprintf("%d package%s, %d with a newer version",
		len(deps), plural(len(deps), "", "s"), updates)
	if showVuln {
		summary += fmt.Sprintf(", %d vulnerable", vulns)
	}
	if updates == 0 && vulns == 0 {
		fmt.Fprintln(out, "\n"+okStyle.Render(summary+" 🎉"))
	} else {
		fmt.Fprintln(out, "\n"+dimStyle.Render(summary))
	}
}

// renderDepsJSON prints the report as a JSON array, stable-sorted, for scripts.
func renderDepsJSON(cmd *cobra.Command, deps []outdatedDep) error {
	type row struct {
		Name    string `json:"name"`
		Current string `json:"current"`
		Latest  string `json:"latest"`
		Update  bool   `json:"updateAvailable"`
		Vuln    string `json:"vulnerability,omitempty"`
		Project string `json:"project,omitempty"`
	}
	rows := make([]row, 0, len(deps))
	for _, d := range deps {
		rows = append(rows, row{d.name, d.current, d.latest, d.hasUpdate(), d.vuln, d.project})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Project != rows[j].Project {
			return rows[i].Project < rows[j].Project
		}
		return rows[i].Name < rows[j].Name
	})
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}
