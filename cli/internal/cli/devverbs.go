package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
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
			// A first arg that names a package scopes the verb to that package.
			if len(args) > 0 {
				ts := discoverWorkspace(cdContext(cmd), root, excludeFor(root))
				if t, ok := matchTarget(ts, args[0]); ok {
					argv, has := devCommandFor(t, verb, root)
					if !has {
						return fmt.Errorf("verb %q has no mapping for ecosystem %q", verb, t.Eco)
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
			// command. Only for --all-capable verbs.
			if supportsAll && len(args) == 0 {
				if handled, herr := offerWorkspaceChoice(cmd, root, verb); handled {
					return herr
				}
			}
			argv, ok := detect.CommandFor(eco, verb, root)
			if !ok {
				return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", verb, eco)
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
// terminal it offers a picker ("All packages" → the --all dashboard, or one
// package); off a TTY it returns a helpful error. handled=false lets the normal
// root command run (single-package repos, or a package at the root).
func offerWorkspaceChoice(cmd *cobra.Command, root, verb string) (handled bool, err error) {
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
		tasks = append(tasks, allTask{name: t.Name, eco: t.Eco, dir: t.Dir, rel: rel, argv: argv})
	}
	// A buildable package at the root, or ≤1 runnable: the normal root command is
	// the right thing — let it run.
	if rootHasPackage || len(tasks) <= 1 {
		return false, nil
	}
	if !interactive() {
		return true, fmt.Errorf(
			"no single %s target here — this is a workspace root with %d packages; run `rig %s --all` or `rig %s <project>`",
			verb, len(tasks), verb, verb)
	}
	switch choice := pickWorkspaceVerbTarget(verb, tasks); choice {
	case pickCancel:
		return true, nil
	case pickAll:
		return true, runAcrossDashboard(cmd, tasks, verb)
	default:
		t := tasks[choice]
		return true, runCommand(cmd, t.dir, t.argv)
	}
}

func filterNote(filter string) string {
	if filter == "" {
		return ""
	}
	return " matching " + filter
}
