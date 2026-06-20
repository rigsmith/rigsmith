package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// newUpgradeCmd builds `rig upgrade` — bulk-upgrade outdated dependencies. The
// target is range-respecting: for an ecosystem with version ranges (node, cargo,
// and Go's in-major tags) it upgrades to the highest version inside each
// manifest's range; .NET, whose PackageReference has no ranges, goes to latest.
// A bare `rig upgrade` on an ecosystem with a machine-readable report (go, node
// [npm/pnpm/bun/yarn-classic], cargo, .NET) discovers what will change and — on
// a TTY, unless --yes — shows the plan and asks for a single confirmation first.
//
// `rig upgrade <pkg>…` targets named packages via the ecosystem's native upgrade
// command (go/node/cargo); .NET, which has no native upgrade verb, takes the
// filtered discover path. yarn berry (no machine-readable report) uses its
// native upgrade directly.
func newUpgradeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "upgrade [packages...]",
		Short: "Upgrade outdated dependencies (within ranges; .NET to latest)",
		Long: "Upgrade outdated dependencies to the highest version inside each\n" +
			"manifest's range (.NET, which has no ranges, upgrades to latest).\n\n" +
			"  rig upgrade               upgrade every outdated dependency\n" +
			"  rig upgrade <pkg>...      upgrade only the named package(s)\n" +
			"  rig upgrade --yes         skip the confirmation prompt\n\n" +
			"On an interactive terminal a bare `rig upgrade` lists what will change " +
			"and asks to confirm before upgrading (pass --yes to skip). To cross a " +
			"range boundary (jump to the newest major), `rig outdated -i` lets you " +
			"pick packages and upgrade them to latest.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			return runUpgrade(cmd, eco, root, yes, args)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "upgrade without the confirmation prompt")
	return cmd
}

// runUpgrade routes `rig upgrade`: a bare invocation on a report-backed
// ecosystem takes the range-respecting plan+confirm flow; targeted upgrades and
// report-less ecosystems take the native command; .NET (no native verb) takes
// the discover path either way.
func runUpgrade(cmd *cobra.Command, eco, root string, yes bool, args []string) error {
	if len(args) == 0 {
		if deps, supported := discoverUpgradable(cmd, eco, root); supported {
			return runBulkUpgrade(cmd, eco, root, deps, yes)
		}
	}

	argv, ok := detect.CommandFor(eco, "upgrade", root)
	if !ok {
		// .NET has no native upgrade command, so a targeted upgrade goes through
		// the discover path filtered to the named packages.
		if eco == detect.DotNet {
			if deps, supported := discoverUpgradable(cmd, eco, root); supported {
				if len(args) > 0 {
					deps = filterDepsByName(deps, args)
				}
				return runBulkUpgrade(cmd, eco, root, deps, yes)
			}
		}
		return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", "upgrade", eco)
	}
	return runCommand(cmd, root, append(argv, args...))
}

// discoverUpgradable returns the dependencies a range-respecting `rig upgrade`
// would change, with each dep's .latest normalized to the in-range target so
// the plan and the per-package commands (go/.NET) use it. supported=false means
// the caller should fall back to the native upgrade (yarn berry, or an
// ecosystem with no report).
func discoverUpgradable(cmd *cobra.Command, eco, root string) (deps []outdatedDep, supported bool) {
	// Cargo: `cargo update --dry-run` is the ground truth for a within-range
	// upgrade — no cargo-outdated needed. (It writes to stderr, so capture both.)
	if eco == detect.Cargo {
		out, _ := captureCombined(cmd, root, "cargo", "update", "--dry-run")
		return parseCargoUpdateDryRun(out), true
	}
	// Yarn berry has no machine-readable outdated report; the caller uses its
	// native upgrade.
	if eco == detect.Node && detect.DetectNodePM(root) == detect.Yarn && yarnIsBerry(cmd, root) {
		return nil, false
	}
	all, ok := discoverOutdated(cmd, eco, root)
	if !ok {
		return nil, false
	}
	// Keep only deps with an in-range upgrade, retargeting .latest to it (the
	// `outdated` report's latest may be a higher, out-of-range major).
	for _, d := range all {
		target := upgradeTarget(d)
		if target == "" || target == d.current {
			continue
		}
		d.latest = target
		deps = append(deps, d)
	}
	return deps, true
}

// upgradeTarget is the range-respecting version to upgrade a dep to: its
// in-range "wanted" version when the ecosystem reports one, else its latest
// (Go's tag is already in-major; .NET has no ranges). Pure.
func upgradeTarget(d outdatedDep) string {
	if d.wanted != "" {
		return d.wanted
	}
	return d.latest
}

// runBulkUpgrade upgrades every dep in deps, gated on a single confirmation when
// interactive (and not --yes). Execution is per-ecosystem: .NET and Go pin each
// package to its target; node and cargo run their native range-respecting bulk
// command. --dry-run prints the commands instead of running them.
func runBulkUpgrade(cmd *cobra.Command, eco, root string, deps []outdatedDep, yes bool) error {
	if len(deps) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render("All dependencies are up to date 🎉"))
		return nil
	}
	cmds := bulkUpgradeCommands(eco, root, deps)
	if len(cmds) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("no upgrade command for this ecosystem"))
		return nil
	}
	if dryRun {
		for _, argv := range cmds {
			echo(cmd, strings.Join(argv, " "))
		}
		return nil
	}
	// Confirm only on a TTY: per the design, a non-interactive (CI / piped) run
	// upgrades straight away, and --yes skips the prompt even on a terminal.
	if !yes && interactive() {
		printUpgradePlan(cmd, deps)
		if !confirmUpgrade(len(deps)) {
			fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("upgrade cancelled"))
			return nil
		}
	}
	return applyUpgrades(cmd, eco, root, cmds, len(deps))
}

// applyUpgrades runs the upgrade commands and reports the outcome. For .NET the
// commands are independent per-package `dotnet add`s, so one that can't be
// applied — a version pinned in an imported .props or Directory.Packages.props —
// is skipped and reported rather than aborting every later project. Other
// ecosystems run interdependent or single bulk commands, where the first failure
// should stop the run. pkgCount is the package total for the non-.NET summary.
func applyUpgrades(cmd *cobra.Command, eco, root string, cmds [][]string, pkgCount int) error {
	if eco == detect.DotNet {
		done, failed := runUpgradeCommands(func(argv []string) error { return runCommand(cmd, root, argv) }, cmds)
		if len(failed) > 0 {
			errw := cmd.ErrOrStderr()
			fmt.Fprintln(errw, warnStyle.Render(fmt.Sprintf("%d package(s) couldn't be upgraded — skipped:", len(failed))))
			for _, argv := range failed {
				fmt.Fprintln(errw, "  "+dimStyle.Render(dotnetAddLabel(argv)))
			}
			fmt.Fprintln(errw, dimStyle.Render("(a version set in an imported .props or Directory.Packages.props can't be changed with `dotnet add`)"))
		}
		fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render(fmt.Sprintf("upgraded %d package(s)", done)))
		return nil
	}
	for _, argv := range cmds {
		if err := runCommand(cmd, root, argv); err != nil {
			return err
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render(fmt.Sprintf("upgraded %d package(s)", pkgCount)))
	return nil
}

// runUpgradeCommands runs each command via run, continuing past failures so one
// un-upgradable package doesn't abort the rest. Returns how many succeeded and
// the commands that failed (each already streamed its own error). run is a seam
// for testing; production passes a runCommand closure.
func runUpgradeCommands(run func(argv []string) error, cmds [][]string) (done int, failed [][]string) {
	for _, argv := range cmds {
		if err := run(argv); err != nil {
			failed = append(failed, argv)
			continue
		}
		done++
	}
	return done, failed
}

// dotnetAddLabel renders a `dotnet add <proj> package <id> --version <ver>` argv
// as "id → ver  (project)" for the skipped-upgrades report. Falls back to the
// joined argv if it doesn't match that shape. Pure.
func dotnetAddLabel(argv []string) string {
	var id, ver, proj string
	for i, a := range argv {
		switch a {
		case "package":
			if i+1 < len(argv) {
				id = argv[i+1]
			}
		case "--version":
			if i+1 < len(argv) {
				ver = argv[i+1]
			}
		case "add":
			if i+1 < len(argv) && argv[i+1] != "package" {
				proj = filepath.Base(argv[i+1])
			}
		}
	}
	if id == "" {
		return strings.Join(argv, " ")
	}
	label := id
	if ver != "" {
		label += " → " + ver
	}
	if proj != "" {
		label += "  (" + proj + ")"
	}
	return label
}

// bulkUpgradeCommands builds the commands a range-respecting `rig upgrade` runs:
// .NET and Go pin each package to its (in-range) target — .NET has no native
// upgrade verb and Go's per-module `go get` is precise — while node and cargo
// run their native range-respecting bulk command (`npm update`, `cargo update`,
// …), which the report-derived plan mirrors.
func bulkUpgradeCommands(eco, root string, deps []outdatedDep) [][]string {
	switch eco {
	case detect.DotNet:
		return dotnetUpgradeCommands(deps)
	case detect.Go:
		return goUpgradeCommands(deps)
	default: // node, cargo: the native bulk command stays within ranges
		if argv, ok := detect.CommandFor(eco, "upgrade", root); ok {
			return [][]string{argv}
		}
		return nil
	}
}

// printUpgradePlan lists the pending upgrades (name current → target, with the
// owning project for .NET) above the confirm prompt.
func printUpgradePlan(cmd *cobra.Command, deps []outdatedDep) {
	w := nameWidth(deps)
	fmt.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("%d package(s) to upgrade:", len(deps)))
	for _, d := range deps {
		fmt.Fprintln(cmd.OutOrStdout(), "  "+outdatedLabel(d, w))
	}
}

// confirmUpgrade asks the single yes/no gate before a bulk upgrade. Returns
// false on decline or esc/ctrl+c.
func confirmUpgrade(n int) bool {
	var ok bool
	c := huh.NewConfirm().
		Title(fmt.Sprintf("Upgrade %d package(s)?", n)).
		Affirmative("Upgrade").
		Negative("Cancel").
		Value(&ok)
	if err := runHuhConfirm(c); err != nil {
		return false
	}
	return ok
}

// filterDepsByName keeps the deps whose name matches one of names (case-
// insensitive). Used for a targeted `rig upgrade <pkg>` on .NET (which has no
// native per-package upgrade verb to forward to). Pure.
func filterDepsByName(deps []outdatedDep, names []string) []outdatedDep {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	var out []outdatedDep
	for _, d := range deps {
		if want[strings.ToLower(d.name)] {
			out = append(out, d)
		}
	}
	return out
}

// nameWidth is the padding width for a picker/plan column: the longest dep name,
// capped at 40. Pure.
func nameWidth(deps []outdatedDep) int {
	w := 0
	for _, d := range deps {
		if n := len(d.name); n > w {
			w = n
		}
	}
	if w > 40 {
		w = 40
	}
	return w
}

// captureCombined runs argv in root with the rig env layering and returns its
// combined stdout+stderr. Used for commands (like `cargo update --dry-run`)
// whose useful output goes to stderr.
func captureCombined(cmd *cobra.Command, root string, argv ...string) (string, error) {
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = root
	c.Env = commandEnv(root)
	out, err := c.CombinedOutput()
	return string(out), err
}
