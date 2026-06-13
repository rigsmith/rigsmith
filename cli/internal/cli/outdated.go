package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newOutdatedCmd builds `rig outdated` — by default a passthrough to the
// ecosystem's outdated report (the historical behavior). With -i/--interactive
// on a terminal it instead discovers the outdated dependencies, lets you pick
// which to upgrade from a checklist, and runs the upgrade. Supported for go,
// node (npm/pnpm), and .NET; other ecosystems/managers fall back to the list.
func newOutdatedCmd() *cobra.Command {
	var pick bool
	cmd := &cobra.Command{
		Use:     "outdated [args]",
		Short:   "List outdated dependencies",
		Aliases: []string{"od"},
		Long: "List outdated dependencies for the detected ecosystem.\n\n" +
			"  rig outdated              list outdated dependencies\n" +
			"  rig outdated -i           pick from the list and upgrade interactively",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			if pick && !dryRun && interactive() {
				return runOutdatedInteractive(cmd, eco, root)
			}
			if pick && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
					"-i needs an interactive terminal — listing instead"))
			}
			return runPlainOutdated(cmd, eco, root, args)
		},
	}
	cmd.Flags().BoolVarP(&pick, "interactive", "i", false,
		"pick outdated deps from a checklist and upgrade them")
	return cmd
}

// runPlainOutdated runs the ecosystem's outdated command and streams it — the
// non-interactive default.
func runPlainOutdated(cmd *cobra.Command, eco, root string, args []string) error {
	argv, ok := detect.CommandFor(eco, "outdated", root)
	if !ok {
		return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", "outdated", eco)
	}
	return runCommand(cmd, root, append(argv, args...))
}

// runOutdatedInteractive discovers outdated deps, lets the user pick which to
// upgrade, and applies the upgrade. Falls back to the plain list when the
// ecosystem/manager isn't supported for interactive upgrade.
func runOutdatedInteractive(cmd *cobra.Command, eco, root string) error {
	deps, supported := discoverOutdated(cmd, eco, root)
	if !supported {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
			"interactive upgrade isn't supported here yet — listing instead"))
		return runPlainOutdated(cmd, eco, root, nil)
	}
	if len(deps) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render("All dependencies are up to date 🎉"))
		return nil
	}

	chosen, ok := pickOutdated(deps)
	if !ok {
		fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("upgrade cancelled"))
		return nil
	}
	if len(chosen) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("nothing selected — upgraded nothing"))
		return nil
	}

	cmds := upgradeCommands(eco, root, chosen)
	if len(cmds) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("no upgrade command for this ecosystem"))
		return nil
	}
	for _, argv := range cmds {
		if err := runCommand(cmd, root, argv); err != nil {
			return err
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render(fmt.Sprintf("upgraded %d package(s)", len(chosen))))
	return nil
}

// discoverOutdated runs the ecosystem's machine-readable outdated report and
// parses it. supported=false means interactive upgrade isn't wired for this
// ecosystem/manager (or the report wasn't parseable) — the caller lists instead.
func discoverOutdated(cmd *cobra.Command, eco, root string) (deps []outdatedDep, supported bool) {
	switch eco {
	case detect.Go:
		// `go list -m -u` is slow-ish (it hits the proxy); the JSON form gives us
		// the Update field per module. Exit code is 0 even with updates.
		out, err := captureOutdated(cmd, root, "go", "list", "-m", "-u", "-json", "all")
		if err != nil && out == "" {
			return nil, false
		}
		return parseGoListUpdates(out), true
	case detect.Node:
		pm := detect.DetectNodePM(root)
		if pm != detect.NPM && pm != detect.PNPM {
			return nil, false // yarn/bun outdated --json shapes differ — not wired yet
		}
		// `npm/pnpm outdated` exits non-zero when anything is outdated; the JSON
		// on stdout is still valid, so the error is ignored.
		out, _ := captureOutdated(cmd, root, string(pm), "outdated", "--json")
		return parseNpmOutdated(out), true
	case detect.DotNet:
		out, err := captureOutdated(cmd, root, "dotnet", "list", "package", "--outdated", "--format", "json")
		if err != nil && out == "" {
			return nil, false
		}
		deps := parseDotnetOutdated(out)
		// An SDK too old for `--format json` prints nothing parseable; treat an
		// empty parse with non-empty output as unsupported so we fall back.
		if deps == nil && strings.TrimSpace(out) != "" && !strings.HasPrefix(strings.TrimSpace(out), "{") {
			return nil, false
		}
		return deps, true
	default:
		return nil, false
	}
}

// pickOutdated shows the outdated deps in an (initially unchecked) multi-select
// and returns the chosen ones. ok=false on esc/ctrl+c.
func pickOutdated(deps []outdatedDep) (chosen []outdatedDep, ok bool) {
	width := 0
	for _, d := range deps {
		if n := len(d.name); n > width {
			width = n
		}
	}
	if width > 40 {
		width = 40
	}
	var selected []int
	opts := make([]huh.Option[int], 0, len(deps))
	for i, d := range deps {
		opts = append(opts, huh.NewOption(outdatedLabel(d, width), i))
	}
	ms := huh.NewMultiSelect[int]().
		Title("Upgrade which packages? (space toggles · enter confirms · esc cancels)").
		Options(opts...).
		Value(&selected)
	if err := runHuhMultiSelect(ms); err != nil {
		return nil, false
	}
	chosen = make([]outdatedDep, 0, len(selected))
	for _, i := range selected {
		chosen = append(chosen, deps[i])
	}
	return chosen, true
}

// outdatedLabel renders one picker row: "name  current → latest" with an
// optional project tag for .NET (where the same package can differ per project).
func outdatedLabel(d outdatedDep, nameW int) string {
	label := fmt.Sprintf("%-*s  %s → %s", nameW, truncateLabel(d.name, nameW), d.current, d.latest)
	if d.project != "" {
		label += dimStyle.Render("  (" + filepath.Base(d.project) + ")")
	}
	return label
}

// upgradeCommands builds the per-ecosystem upgrade command(s) for the chosen
// deps.
func upgradeCommands(eco, root string, chosen []outdatedDep) [][]string {
	switch eco {
	case detect.Go:
		return goUpgradeCommands(chosen)
	case detect.Node:
		return nodeUpgradeCommands(detect.DetectNodePM(root), chosen)
	case detect.DotNet:
		return dotnetUpgradeCommands(chosen)
	default:
		return nil
	}
}

// captureOutdated runs argv in root with the rig env layering and returns its
// stdout. Commands like `npm outdated` exit non-zero by design, so callers that
// expect that ignore the error and parse stdout.
func captureOutdated(cmd *cobra.Command, root string, argv ...string) (string, error) {
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = root
	c.Env = commandEnv(root)
	out, err := c.Output()
	return string(out), err
}
