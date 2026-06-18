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
		all         bool
		filter      string
		watch       bool
		interactive bool
		presets     []presetFlag
	)
	cmd := &cobra.Command{
		Use:               verb + " [project]",
		Short:             short,
		Aliases:           aliases,
		ValidArgsFunction: verbCompletion(verb),
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
			// `-i`/`--interactive` (no project arg) always opens the picker — even
			// when a single target would otherwise run directly. `run` lists every
			// runnable package and surfaced script; the --all verbs list every
			// package (with "All packages"). With an explicit project arg the arg
			// wins (below).
			if interactive && len(args) == 0 && (supportsAll || verb == "run") {
				if handled, herr := offerWorkspaceChoice(cmd, root, verb, supportsAll, true); handled {
					return herr
				}
			}
			// A first arg that names a package scopes the verb to that package.
			// `run` matches against the per-binary expansion so `rig run rig`
			// resolves a cmd/rig main, not just a module.
			if len(args) > 0 {
				var ts []target
				if verb == "run" {
					ts = runTargets(cdContext(cmd), root)
				} else {
					ts = discoverWorkspace(cdContext(cmd), root, excludeFor(root))
				}
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
			// Default: the primary ecosystem at the repo root. Resolve it, but
			// defer a failure — a repo can have no single primary (e.g. .NET
			// solutions nested in subdirs with nothing at the root) yet still have
			// runnable subprojects the picker below can offer.
			eco, ecoErr := resolvePrimary(cwd, root)
			if ecoErr == nil && verb == "rebuild" {
				return runRebuild(cmd, eco, root, args)
			}
			// `rig test <class|~filter>` in a .NET repo: an arg that names no
			// package is a test-class query / filter shorthand (TestVerb).
			if ecoErr == nil && verb == "test" && eco == detect.DotNet && len(args) > 0 {
				return runDotnetTest(cmd, root, args, false)
			}
			// A bare verb at a workspace root (packages only in subdirs) has no
			// single target — offer a picker instead of running a doomed root
			// command. For --all-capable verbs the picker leads with "All
			// packages"; `run` gets a single-select of the runnable packages. This
			// runs before the primary is required, so it works even when no
			// primary resolves (the picker scopes the verb to a chosen package).
			if len(args) == 0 && (supportsAll || verb == "run") {
				if handled, herr := offerWorkspaceChoice(cmd, root, verb, supportsAll, false); handled {
					return herr
				}
			}
			// The picker didn't handle it — the root command needs the primary, so
			// surface the unresolved-ecosystem error now.
			if ecoErr != nil {
				return ecoErr
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
		cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, usage)
	}
	presets = registerPresetFlags(cmd)
	return cmd
}

// verbCompletion picks the [project] completion source for a verb. `run`
// resolves its arg against the per-binary run targets (runTargets), so its
// completion must too — otherwise `rig run <TAB>` would suggest Go module names
// while `rig run <name>` only matches the expanded cmd/* binaries. Every other
// verb completes against the module-level workspace names.
func verbCompletion(verb string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	if verb == "run" {
		return runTargetCompletion
	}
	return workspaceNameCompletion
}

// runTargetCompletion completes `rig run`'s [project] arg with the expanded run
// targets (cmd/rig, cmd/clauderig, … for a multi-binary Go repo), matching how
// the arg is resolved. Never errors: completion must never break the shell.
func runTargetCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	ts := runTargets(cdContext(cmd), root)
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
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
// forcePick (`-i`/`--interactive`) always shows the picker: a runnable root package (and,
// for `run`, the surfaced scripts) is included even when one obvious target
// exists, and a single candidate still opens the picker rather than running.
func offerWorkspaceChoice(cmd *cobra.Command, root, verb string, offerAll, forcePick bool) (handled bool, err error) {
	// `run` expands each Go module into its individual binaries (cmd/rig, …) so a
	// multi-binary repo offers each one; other verbs operate at the module level.
	var raw []target
	if verb == "run" {
		raw = runTargets(cdContext(cmd), root)
	} else {
		raw = discoverWorkspace(cdContext(cmd), root, excludeFor(root))
	}
	targets := topoSort(filterTargets(raw, ""))

	// `run` additionally offers the repo's surfaced scripts (package.json
	// scripts, .rig.json commands, Go scripts//cmd verbs) as a second group — the
	// same scripts `rig <name>` runs. Those Go script verbs (scripts/…, go.work
	// cmd entries) are also main packages, so they'd appear in the expanded run
	// targets too; their directories are collected here to dedup them out of the
	// Projects group, keeping each binary in exactly one group.
	var scripts []scriptEntry
	var defaultProject string
	scriptDirs := map[string]bool{}
	if verb == "run" {
		cfg, _ := config.LoadMerged(root)
		defaultProject = cfg.DefaultProject
		scripts = discoverScripts(root, cfg)
		for _, e := range scripts {
			if e.eco == "go" {
				scriptDirs[e.loc] = true
			}
		}
	}

	var tasks []allTask
	rootHasPackage := false
	for _, t := range targets {
		rel, rerr := filepath.Rel(root, t.Dir)
		if rerr != nil {
			rel = t.Dir
		}
		rel = filepath.ToSlash(rel)
		argv, ok := devCommandFor(t, verb, root)
		if !ok {
			continue
		}
		if verb == "run" {
			// For `run`, list only packages that are actually runnable (a Go dir
			// with a main package, etc.) so libraries don't clutter the picker.
			if !isRunnable(t) {
				continue
			}
			// A binary already surfaced as a script verb belongs to the Scripts
			// group; don't also list it under Projects.
			if t.Eco == detect.Go && scriptDirs[rel] {
				continue
			}
		}
		// A target at the repo root only counts as "the root package" once it
		// has cleared the same filters that admit it to tasks. A Go module whose
		// mains live under cmd/ (no `package main` at the root) is not runnable,
		// so for `run` it must not suppress the picker + scripts by masquerading
		// as a runnable root — otherwise rig falls through to a doomed `go run .`.
		if rel == "." {
			rootHasPackage = true
		}
		tasks = append(tasks, allTask{name: t.Name, eco: t.Eco, dir: t.Dir, rel: rel, argv: argv})
	}

	// A directly-runnable root (a Go module with a `package main` at its root, or
	// a single-package app) runs on a plain `rig run` — only show the surfaced
	// scripts alongside it when the user explicitly opens the picker.
	if verb == "run" && rootHasPackage && !forcePick {
		scripts = nil
	}

	if forcePick {
		// `-i`/`--interactive`: always the picker, including a runnable root package.
		if verb == "run" {
			if len(tasks)+len(scripts) == 0 {
				return true, fmt.Errorf("nothing runnable here to pick from")
			}
			return offerRunChoice(cmd, root, tasks, scripts, defaultProject, true)
		}
		if len(tasks) == 0 {
			return true, fmt.Errorf("no %s targets here to pick from", verb)
		}
		if !interactive() {
			return true, fmt.Errorf("-i/--interactive needs an interactive terminal; run `rig %s <project>`", verb)
		}
		return dispatchVerbPick(cmd, verb, tasks, offerAll)
	}

	// A buildable package at the root, or nothing to offer: the normal root
	// command is the right thing — let it run (or produce its own error).
	if rootHasPackage || len(tasks)+len(scripts) == 0 {
		return false, nil
	}

	if verb == "run" {
		return offerRunChoice(cmd, root, tasks, scripts, defaultProject, false)
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
// packages and surfaced scripts. A configured defaultProject naming a runnable
// package wins outright; otherwise a single target runs directly and several
// open the grouped picker (Projects, then Scripts). Off a TTY it returns a
// helpful error. With forcePick set (`-i`/`--interactive`) the picker always opens, even
// when a default or a lone candidate would otherwise run.
func offerRunChoice(cmd *cobra.Command, root string, tasks []allTask, scripts []scriptEntry, defaultProject string, forcePick bool) (handled bool, err error) {
	if !forcePick {
		if t, ok := preferredRunTask(tasks, defaultProject); ok {
			return true, runCommand(cmd, t.dir, t.argv)
		}
		if len(tasks)+len(scripts) == 1 {
			if len(tasks) == 1 {
				t := tasks[0]
				return true, runCommand(cmd, t.dir, t.argv)
			}
			return true, scripts[0].run(cmd, nil)
		}
	}
	if !interactive() {
		// Off a TTY there's no picker. Point at defaultProject when it's the
		// relevant lever: with -i it's what a plain `rig run` would use; without
		// it, a configured-but-unmatched default is why we're here.
		switch {
		case forcePick && defaultProject != "":
			return true, fmt.Errorf(
				"-i/--interactive needs an interactive terminal; without it `rig run` uses the configured defaultProject %q, or name one: `rig run <project>`",
				defaultProject)
		case forcePick:
			return true, fmt.Errorf("-i/--interactive needs an interactive terminal; run `rig run <project>` instead")
		case defaultProject != "":
			return true, fmt.Errorf(
				"configured defaultProject %q doesn't match a runnable project here — run `rig run <project>` or update the default",
				defaultProject)
		default:
			return true, fmt.Errorf(
				"no single run target here — this is a workspace root with %s and %s; run `rig run <project>` or `rig <script>`",
				pluralN(len(tasks), "package"), pluralN(len(scripts), "script"))
		}
	}
	switch sel := pickRunTarget(cdContext(cmd), root, scripts); {
	case sel.cancel:
		return true, nil
	case sel.script != nil:
		return true, sel.script.run(cmd, nil)
	case sel.task != nil:
		return true, runCommand(cmd, sel.task.dir, sel.task.argv)
	default:
		return true, nil
	}
}

// preferredRunTask finds the task matching the configured defaultProject by
// full name or short name (case-insensitive). The short name is matched both
// slash-segmented (node scopes) and dot-segmented (.NET project names like
// "Acme.Desktop" → "Desktop"). ok=false when no default is set or it names no
// runnable task — callers then fall back to the picker.
func preferredRunTask(tasks []allTask, defaultProject string) (allTask, bool) {
	q := strings.TrimSpace(defaultProject)
	if q == "" {
		return allTask{}, false
	}
	for _, t := range tasks {
		if strings.EqualFold(t.name, q) ||
			strings.EqualFold(shortName(t.name), q) ||
			strings.EqualFold(dotShortName(t.name), q) {
			return t, true
		}
	}
	return allTask{}, false
}

// dotShortName is the segment after the last '.' (a .NET project's short name).
func dotShortName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isRunnable reports whether a workspace target can actually be run — it filters
// the `run` picker so libraries don't appear. Precise for Go (looks for a
// `package main`); other ecosystems pass through (their `run` mapping is the
// gate).
func isRunnable(t target) bool {
	switch t.Eco {
	case detect.Go:
		return goDirHasMain(t.Dir)
	case detect.DotNet:
		// Discovery classified it from the csproj <OutputType> (Exe/WinExe and
		// not a test project) — libraries and test projects aren't run targets.
		return t.Runnable
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
