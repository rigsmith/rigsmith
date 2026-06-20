package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
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
		Use:     "outdated [project]",
		Short:   "List outdated dependencies",
		Aliases: []string{"od"},
		Long: "List outdated dependencies for the detected ecosystem.\n\n" +
			"For .NET this reviews every project in the repo (respecting \"exclude\"),\n" +
			"so a stale package in any in-repo project surfaces; name a project to\n" +
			"scope it, like `rig run <project>`.\n\n" +
			"  rig outdated              review the whole repo\n" +
			"  rig outdated <project>    scope to one project\n" +
			"  rig outdated -i           pick from the list and upgrade interactively",
		ValidArgsFunction: outdatedProjectCompletion,
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

// outdatedProjectCompletion completes `rig outdated`'s [project] arg with the
// repo's .NET project names. The arg is .NET-only — other ecosystems review the
// whole module in one call — and it resolves through dotnetReviewProjects, which
// matches a project's name or short name, so completing the full names suffices.
func outdatedProjectCompletion(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	cfg, _ := config.LoadMerged(root)
	var names []string
	for _, p := range detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude) {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// runPlainOutdated runs the ecosystem's outdated command and streams it — the
// non-interactive default.
func runPlainOutdated(cmd *cobra.Command, eco, root string, args []string) error {
	if eco == detect.Cargo {
		if _, err := toolCargoOutdated.require(cmd, root); err != nil {
			return err
		}
	}
	// .NET reports per project (or per solution), so a single `dotnet list` at a
	// workspace root sees only one project's direct packages — a stale package in
	// an in-repo dependency would be missed. Review every project instead (scoped
	// to one when named), and render the aggregated table so you can see which
	// project each outdated package lives in.
	if eco == detect.DotNet {
		return runDotnetOutdated(cmd, root, args)
	}
	argv, ok := detect.CommandFor(eco, "outdated", root)
	if !ok {
		return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", "outdated", eco)
	}
	return runCommand(cmd, root, append(argv, args...))
}

// runDotnetOutdated reviews the repo's .NET projects (every one respecting
// "exclude", or the single project named in args) and prints the outdated
// packages as one aggregated table — the project column shows where each lives.
func runDotnetOutdated(cmd *cobra.Command, root string, args []string) error {
	cfg, _ := config.LoadMerged(root)
	projectArg := ""
	if len(args) > 0 {
		projectArg = args[0]
	}
	projects, err := dotnetReviewProjects(root, cfg, projectArg)
	if err != nil {
		return err
	}
	deps := dotnetOutdatedAcross(cmd, root, projects)
	if len(deps) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render("All dependencies are up to date 🎉"))
		return nil
	}
	renderDepsTable(cmd, deps, false)
	return nil
}

// dotnetReviewProjects is the set of .NET projects a dep command reviews: the
// whole repo (every project from discovery, respecting "solution"/"exclude"),
// or the single project named by projectArg — the `rig run <project>` model.
// An unmatched name or an empty repo returns an actionable error rather than a
// bare dotnet failure.
func dotnetReviewProjects(root string, cfg config.Config, projectArg string) ([]detect.ProjectInfo, error) {
	projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	if len(projects) == 0 {
		return nil, dotnetNoProjectsError(root, cfg)
	}
	if q := strings.TrimSpace(projectArg); q != "" {
		for _, p := range projects {
			if strings.EqualFold(p.Name, q) || strings.EqualFold(p.ShortName(), q) {
				return []detect.ProjectInfo{p}, nil
			}
		}
		return nil, fmt.Errorf("no .NET project named %q here. Projects rig sees: %s", q, projectNameSample(projects, 12))
	}
	return projects, nil
}

// dotnetOutdatedAcross runs the outdated report for each project and concatenates
// the rows; each row carries its project (from the dotnet JSON) so duplicates
// across projects stay distinct in the table.
func dotnetOutdatedAcross(cmd *cobra.Command, root string, projects []detect.ProjectInfo) []outdatedDep {
	var all []outdatedDep
	for _, out := range dotnetListAcross(cmd, root, projects, "--outdated", "--format", "json") {
		all = append(all, parseDotnetOutdated(out)...)
	}
	return all
}

// dotnetListAcross runs `dotnet list <project> package <listArgs...>` for every
// project and returns each one's stdout in project order. A repo can hold dozens
// of .NET projects (vendored copies, samples, tests), and each `dotnet list`
// takes ~1s, so doing them one at a time turns a whole-repo review into a 30s+
// wait that reads as a hang. Fan the calls out (bounded) and show a live
// project counter so the wait is visibly progress, not a stall.
func dotnetListAcross(cmd *cobra.Command, root string, projects []detect.ProjectInfo, listArgs ...string) []string {
	outs := make([]string, len(projects))
	var done int64
	stop := startReviewProgress(cmd, len(projects), &done)
	sem := make(chan struct{}, dotnetReviewConcurrency())
	var wg sync.WaitGroup
	for i, p := range projects {
		wg.Add(1)
		go func(i int, p detect.ProjectInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			argv := append([]string{"dotnet", "list", p.FullPath, "package"}, listArgs...)
			outs[i], _ = captureOutdated(cmd, root, argv...)
			atomic.AddInt64(&done, 1)
		}(i, p)
	}
	wg.Wait()
	stop()
	return outs
}

// startReviewProgress shows that a whole-repo .NET review is underway and how far
// along it is, then returns a stop func to call once the work is done. On a TTY
// it animates a spinner with a live "k/N projects" counter (read from done);
// piped or under --quiet it degrades to one static line (or silence), matching
// captureWithSpinner. A single project isn't worth announcing.
func startReviewProgress(cmd *cobra.Command, total int, done *int64) func() {
	if total <= 1 {
		return func() {}
	}
	w := cmd.ErrOrStderr()
	if quiet || !writerIsTerminal(w) {
		if !quiet {
			fmt.Fprintln(w, dimStyle.Render(
				fmt.Sprintf("Reviewing %d .NET projects…", total)))
		}
		return func() {}
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(80 * time.Millisecond)
	stopped := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-stopped:
				fmt.Fprint(w, "\r\033[K")
				return
			case <-ticker.C:
				label := fmt.Sprintf("reviewing .NET projects… %d/%d", atomic.LoadInt64(done), total)
				fmt.Fprintf(w, "\r\033[K%s %s", spinnerStyle.Render(frames[i%len(frames)]), dimStyle.Render(label))
			}
		}
	}()
	return func() { close(stopped); <-finished }
}

// dotnetReviewConcurrency caps how many `dotnet list` calls run at once. dotnet
// is itself multi-threaded, so oversubscribing the cores hurts; 8 matched the
// best wall-time in practice without thrashing.
func dotnetReviewConcurrency() int {
	if n := runtime.NumCPU(); n < 8 {
		return n
	}
	return 8
}

// dotnetNoProjectsError explains why a .NET dep review found nothing and how to
// fix it — rig's job is to answer "how do I fix this?", not relay dotnet's error.
func dotnetNoProjectsError(root string, cfg config.Config) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no .NET projects to review under %s", root)
	if len(cfg.Exclude) > 0 {
		fmt.Fprintf(&b, " (after %d \"exclude\" pattern(s))", len(cfg.Exclude))
	}
	b.WriteString(".\n")
	fmt.Fprintf(&b, "Run rig from a directory with a .NET project, or check \"exclude\"/\"solution\" in %s.", config.FileName)
	return errors.New(b.String())
}

// projectNameSample joins up to max project names, summarizing any remainder.
func projectNameSample(projects []detect.ProjectInfo, max int) string {
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(names[:max], ", "), len(names)-max)
}

// runOutdatedInteractive discovers outdated deps, lets the user pick which to
// upgrade, and applies the upgrade. Falls back to the plain list when the
// ecosystem/manager isn't supported for interactive upgrade.
func runOutdatedInteractive(cmd *cobra.Command, eco, root string) error {
	// Yarn Berry has no machine-readable outdated report; hand off to its
	// built-in interactive upgrader (`yarn up -i`), the native equivalent.
	if eco == detect.Node && detect.DetectNodePM(root) == detect.Yarn && yarnIsBerry(cmd, root) {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
			"yarn (berry) has no outdated report — using its built-in `yarn up -i`"))
		return runCommand(cmd, root, []string{"yarn", "up", "-i", "*"})
	}

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
		// `outdated` exits non-zero when anything is outdated; the report on
		// stdout is still valid, so the error is ignored.
		switch pm {
		case detect.NPM, detect.PNPM:
			out, _ := captureOutdated(cmd, root, string(pm), "outdated", "--json")
			return parseNpmOutdated(out), true
		case detect.Bun:
			// bun has no --json; it prints a pipe-delimited ASCII table.
			out, _ := captureOutdated(cmd, root, "bun", "outdated")
			return parseBunOutdated(out), true
		case detect.Yarn:
			// Classic only (berry is handled before discovery). yarn v1 emits
			// NDJSON; the table row carries the outdated packages.
			out, _ := captureOutdated(cmd, root, "yarn", "outdated", "--json")
			return parseYarnClassicOutdated(out), true
		default:
			return nil, false
		}
	case detect.DotNet:
		cfg, _ := config.LoadMerged(root)
		projects, perr := dotnetReviewProjects(root, cfg, "")
		if perr != nil {
			return nil, false // no projects — caller falls back to the guided list
		}
		return dotnetOutdatedAcross(cmd, root, projects), true
	default:
		return nil, false
	}
}

// pickOutdated shows the outdated deps in an (initially unchecked) multi-select
// and returns the chosen ones. ok=false on esc/ctrl+c.
func pickOutdated(deps []outdatedDep) (chosen []outdatedDep, ok bool) {
	width := nameWidth(deps)
	var selected []int
	opts := make([]huh.Option[int], 0, len(deps))
	for i, d := range deps {
		opts = append(opts, huh.NewOption(outdatedLabel(d, width), i))
	}
	ms := huh.NewMultiSelect[int]().
		Title("Upgrade which packages? (space toggles · ctrl+a all · / filter · enter confirms · esc cancels)").
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
		switch pm := detect.DetectNodePM(root); pm {
		case detect.Bun:
			return bunUpgradeCommands(chosen) // preserves dev vs prod sections
		case detect.Yarn:
			return yarnUpgradeCommands(chosen) // classic; --latest keeps sections
		default:
			return nodeUpgradeCommands(pm, chosen)
		}
	case detect.DotNet:
		return dotnetUpgradeCommands(chosen)
	default:
		return nil
	}
}

// yarnIsBerry reports whether the project's yarn is v2+ (berry), which has no
// `outdated` command. It asks `yarn --version`, falling back to the presence of
// a `.yarnrc.yml` (berry's config file; classic uses `.yarnrc`).
func yarnIsBerry(cmd *cobra.Command, root string) bool {
	if out, err := captureOutdated(cmd, root, "yarn", "--version"); err == nil {
		major, _, _ := strings.Cut(strings.TrimSpace(out), ".")
		if n, e := strconv.Atoi(major); e == nil {
			return n >= 2
		}
	}
	return fileExists(filepath.Join(root, ".yarnrc.yml"))
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
