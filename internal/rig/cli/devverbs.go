package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// devVerbCmd builds a dev-loop verb (build/test/run/format/lint/typecheck/clean)
// with workspace awareness: an optional project selector scopes the verb to one
// package's directory, and (when supportsAll) `--all` runs it across every
// workspace package in dependency order, narrowable with `--filter`.
func devVerbCmd(verb, short string, supportsAll bool, aliases ...string) *cobra.Command {
	var (
		all     bool
		filter  string
		watch   bool
		pick    bool
		presets []presetFlag
	)
	cmd := &cobra.Command{
		Use:               verb + " [project]",
		Short:             short,
		Aliases:           aliases,
		ValidArgsFunction: workspaceNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			// Activate any selected env presets for this run (applied as the top
			// env layer in commandEnv).
			presetEnv = activePresetEnv(root, presets)

			if all {
				if watch {
					return fmt.Errorf("--watch cannot be combined with --all")
				}
				return runAcross(cmd, root, verb, filter, args)
			}
			// `--watch` at any flag position reroutes through the watch path
			// (the trailing form is folded onto the `watch` subcommand by the
			// pre-parse pipeline; both land in runWatchVerb).
			if watch {
				return runWatchVerb(cmd, verb, args)
			}
			// `--pick` (no project arg) always opens the picker — even when a single
			// target would otherwise run directly. `run` lists every runnable
			// package and surfaced script; the --all verbs list every package (with
			// "All packages"). With an explicit project arg the arg wins (below).
			if pick && len(args) == 0 && (supportsAll || verb == "run") {
				if handled, herr := offerWorkspaceChoice(cmd, root, verb, supportsAll, true); handled {
					return herr
				}
			}
			// A first arg that names a package scopes the verb to that package.
			if len(args) > 0 {
				ts := discoverWorkspace(cdContext(cmd), root, excludeFor(root))
				if t, ok := matchTarget(ts, args[0]); ok {
					argv, has := devCommandFor(t, verb, root)
					if !has {
						return fmt.Errorf("verb %q has no mapping for ecosystem %q", verb, t.Eco)
					}
					if verb == "format" {
						if err := requireDotnetFormatter(cmd, t.Eco, root); err != nil {
							return err
						}
					}
					return runCommand(cmd, t.Dir, append(argv, args[1:]...))
				}
			}
			// Default: the primary ecosystem at the repo root.
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			if verb == "rebuild" {
				return runRebuild(cmd, eco, root, args)
			}
			// `rig test <class|~filter>` in a .NET repo: an arg that names no
			// package is a test-class query / filter shorthand (TestVerb).
			if verb == "test" && eco == detect.DotNet && len(args) > 0 {
				return runDotnetTest(cmd, root, args, false)
			}
			// A bare verb at a workspace root (packages only in subdirs) has no
			// single target — offer a picker instead of running a doomed root
			// command. For --all-capable verbs the picker leads with "All
			// packages"; `run` gets a single-select of the runnable packages.
			if len(args) == 0 && (supportsAll || verb == "run") {
				if handled, herr := offerWorkspaceChoice(cmd, root, verb, supportsAll, false); handled {
					return herr
				}
			}
			argv, ok := resolveVerbCommand(eco, verb, root)
			if !ok {
				return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", verb, eco)
			}
			if verb == "format" {
				if err := requireDotnetFormatter(cmd, eco, root); err != nil {
					return err
				}
			}
			return runCommand(cmd, root, append(argv, args...))
		},
	}
	if supportsAll {
		cmd.Flags().BoolVarP(&all, "all", "a", false, "run across every workspace package (dependency order)")
		cmd.Flags().StringVar(&filter, "filter", "", "with --all, limit to packages matching this glob")
	}
	if watchableVerb(verb) {
		cmd.Flags().BoolVarP(&watch, "watch", "w", false, "run in the ecosystem's watch mode (re-run on change)")
	}
	if supportsAll || verb == "run" {
		usage := "always open the picker (choose a package)"
		if verb == "run" {
			usage = "always open the picker (choose a package or script to run)"
		}
		cmd.Flags().BoolVarP(&pick, "pick", "p", false, usage)
	}
	presets = registerPresetFlags(cmd)
	return cmd
}

// workspaceNameCompletion completes a [project] arg with the discovered
// workspace package names (every ecosystem). It is the dynamic-completion
// source shared by the dev verbs and kill — the Go analogue of the .NET rig's
// Completions wiring (cobra owns the shell protocol there played by the
// [suggest] directive).
func workspaceNameCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	ts := discoverWorkspace(cdContext(cmd), root, excludeFor(root))
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// runnableProjectCompletion completes a [project] arg with the repo's runnable
// .NET project short names — the .NET rig's Completions.RunnableProjects, for
// the verbs whose positional resolves through resolveRunProject (publish,
// default). Never errors: completion must never break the shell.
func runnableProjectCompletion(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	cfg, _ := config.LoadMerged(root)
	var names []string
	for _, p := range detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude) {
		if p.IsRunnable() {
			names = append(names, p.ShortName())
		}
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// runAcross runs the verb in every workspace package (topo order), optionally
// filtered, skipping packages whose ecosystem doesn't map the verb. On an
// interactive terminal it renders a live dashboard (per-package status +
// streamed output); otherwise it streams sequentially as plain output.
func runAcross(cmd *cobra.Command, root, verb, filter string, args []string) error {
	targets := topoSort(filterTargets(discoverWorkspace(cdContext(cmd), root, excludeFor(root)), filter))
	if len(targets) == 0 {
		return fmt.Errorf("no workspace packages found%s", filterNote(filter))
	}
	// Resolve the runnable tasks up front (packages whose ecosystem maps the verb).
	var tasks []allTask
	for _, t := range targets {
		argv, ok := devCommandFor(t, verb, root)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(root, t.Dir)
		if err != nil {
			rel = t.Dir
		}
		tasks = append(tasks, allTask{name: t.Name, eco: t.Eco, dir: t.Dir, rel: rel, argv: append(argv, args...)})
	}
	if len(tasks) == 0 {
		return fmt.Errorf("no workspace package maps verb %q", verb)
	}

	if allDashboardEligible() {
		return runAcrossDashboard(cmd, tasks, verb)
	}

	// Plain sequential path (CI, piped, --quiet, --dry-run): abort on first failure.
	out := cmd.OutOrStdout()
	for _, t := range tasks {
		fmt.Fprintln(out, dimStyle.Render(fmt.Sprintf("· %s (%s)", t.name, t.eco)))
		if err := runCommand(cmd, t.dir, t.argv); err != nil {
			return fmt.Errorf("%s in %s: %w", verb, t.name, err)
		}
	}
	return nil
}

// offerWorkspaceChoice handles a bare dev verb at a workspace root: when packages
// live only in subdirectories (no buildable package at the root) and several
// exist, running the verb at the root has no single target. On an interactive
// terminal it offers a picker (with "All packages" → the --all dashboard when
// offerAll is set, e.g. build/test; `run` gets a grouped picker of runnable
// packages and surfaced scripts); off a TTY it returns a helpful error.
// handled=false lets the normal root command run (single-package repos, or a
// package at the root).
//
// forcePick (`--pick`) always shows the picker: a runnable root package (and,
// for `run`, the surfaced scripts) is included even when one obvious target
// exists, and a single candidate still opens the picker rather than running.
func offerWorkspaceChoice(cmd *cobra.Command, root, verb string, offerAll, forcePick bool) (handled bool, err error) {
	targets := topoSort(filterTargets(discoverWorkspace(cdContext(cmd), root, excludeFor(root)), ""))
	var tasks []allTask
	rootHasPackage := false
	for _, t := range targets {
		rel, rerr := filepath.Rel(root, t.Dir)
		if rerr != nil {
			rel = t.Dir
		}
		if rel == "." {
			rootHasPackage = true
		}
		argv, ok := devCommandFor(t, verb, root)
		if !ok {
			continue
		}
		// For `run`, list only packages that are actually runnable (a Go module
		// with a main package, etc.) so libraries don't clutter the picker.
		if verb == "run" && !isRunnable(t) {
			continue
		}
		tasks = append(tasks, allTask{name: t.Name, eco: t.Eco, dir: t.Dir, rel: rel, argv: argv})
	}

	// `run` additionally offers the repo's surfaced scripts (package.json
	// scripts, .rig.json commands, Go scripts//cmd verbs) as a second group —
	// the same scripts `rig <name>` runs. Other verbs don't map to scripts.
	var scripts []scriptEntry
	if verb == "run" && (forcePick || !rootHasPackage) {
		cfg, _ := config.LoadMerged(root)
		scripts = discoverScripts(root, cfg)
	}

	if forcePick {
		// `--pick`: always the picker, including a runnable root package.
		if verb == "run" {
			if len(tasks)+len(scripts) == 0 {
				return true, fmt.Errorf("nothing runnable here to pick from")
			}
			return offerRunChoice(cmd, tasks, scripts, true)
		}
		if len(tasks) == 0 {
			return true, fmt.Errorf("no %s targets here to pick from", verb)
		}
		if !interactive() {
			return true, fmt.Errorf("--pick needs an interactive terminal; run `rig %s <project>`", verb)
		}
		return dispatchVerbPick(cmd, verb, tasks, offerAll)
	}

	// A buildable package at the root, or nothing to offer: the normal root
	// command is the right thing — let it run (or produce its own error).
	if rootHasPackage || len(tasks)+len(scripts) == 0 {
		return false, nil
	}

	if verb == "run" {
		return offerRunChoice(cmd, tasks, scripts, false)
	}

	// --all-capable verbs (build/test/…): a lone subpackage falls through to the
	// root command; several open the package picker (with "All packages").
	if len(tasks) == 1 {
		return false, nil
	}
	if !interactive() {
		hint := "run `rig " + verb + " <project>`"
		if offerAll {
			hint = "run `rig " + verb + " --all` or `rig " + verb + " <project>`"
		}
		return true, fmt.Errorf(
			"no single %s target here — this is a workspace root with %d packages; %s",
			verb, len(tasks), hint)
	}
	return dispatchVerbPick(cmd, verb, tasks, offerAll)
}

// dispatchVerbPick shows the package picker for an --all-capable verb and runs
// the choice: "All packages" → the --all dashboard, a package → its command,
// cancel → nothing. Shared by the implicit multi-package case and `--pick`.
func dispatchVerbPick(cmd *cobra.Command, verb string, tasks []allTask, offerAll bool) (handled bool, err error) {
	switch choice := pickWorkspaceVerbTarget(verb, tasks, offerAll); choice {
	case pickCancel:
		return true, nil
	case pickAll:
		return true, runAcrossDashboard(cmd, tasks, verb)
	default:
		t := tasks[choice]
		return true, runCommand(cmd, t.dir, t.argv)
	}
}

// offerRunChoice resolves a bare `rig run` at a workspace root over the runnable
// packages and surfaced scripts. A single target runs directly; several open the
// grouped picker (Projects, then Scripts). Off a TTY it returns a helpful error.
// With forcePick set (`--pick`) the picker always opens, even for one candidate.
func offerRunChoice(cmd *cobra.Command, tasks []allTask, scripts []scriptEntry, forcePick bool) (handled bool, err error) {
	if !forcePick && len(tasks)+len(scripts) == 1 {
		if len(tasks) == 1 {
			t := tasks[0]
			return true, runCommand(cmd, t.dir, t.argv)
		}
		return true, scripts[0].run(cmd, nil)
	}
	if !interactive() {
		return true, fmt.Errorf(
			"no single run target here — this is a workspace root with %s and %s; run `rig run <project>` or `rig <script>`",
			pluralN(len(tasks), "package"), pluralN(len(scripts), "script"))
	}
	switch sel := pickRunTarget(tasks, scripts); {
	case sel.cancel:
		return true, nil
	case sel.script:
		return true, scripts[sel.index].run(cmd, nil)
	default:
		t := tasks[sel.index]
		return true, runCommand(cmd, t.dir, t.argv)
	}
}

// isRunnable reports whether a workspace target can actually be run — it filters
// the `run` picker so libraries don't appear. Precise for Go (looks for a
// `package main`); other ecosystems pass through (their `run` mapping is the
// gate).
func isRunnable(t target) bool {
	if t.Eco == detect.Go {
		return goDirHasMain(t.Dir)
	}
	return true
}

// goDirHasMain reports whether dir has a buildable `package main` — a non-test
// *.go file at the dir root whose package clause is `main`. Best-effort.
func goDirHasMain(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range matches {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if fileDeclaresMainPackage(f) {
			return true
		}
	}
	return false
}

// fileDeclaresMainPackage reports whether a .go file's package clause is `main`.
// The first non-blank, non-comment line of a Go file is its `package` clause, so
// scanning to it is enough. Best-effort (line comments / build tags skipped).
func fileDeclaresMainPackage(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		return line == "package main"
	}
	return false
}

func filterNote(filter string) string {
	if filter == "" {
		return ""
	}
	return " matching " + filter
}
