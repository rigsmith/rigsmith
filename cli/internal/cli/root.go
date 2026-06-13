// Package cli defines rig's command surface with charm/fang styling. It covers
// the everyday dev verbs (run/build/test/format/lint/typecheck), an interactive
// menu (ui), repo discovery (info), and per-repo custom commands from .rig.json.
// The remaining feature-parity surface (coverage, kill, package management,
// completion, cross-ecosystem delegation) is tracked in docs/PORTING-PLAN.md.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/rigsmith/cli/internal/envstack"
	"github.com/spf13/cobra"
)

var dimStyle = lipgloss.NewStyle().Foreground(brandMuted)

var (
	dryRun   bool
	quiet    bool
	noEnv    bool   // --no-env: skip .env/.env.local loading
	rootFlag string // --root: override walk-up root resolution

	// presetEnv holds the env vars contributed by the active `.rig.json` env
	// presets (set per run from the dev-verb preset flags); merged as the top
	// layer of the spawned-process environment.
	presetEnv map[string]string
)

// Execute builds and runs the rig command tree.
func Execute(ctx context.Context) error {
	// Unambiguous verb prefixes resolve (e.g. `rig cove` → coverage, `rig reb` → rebuild).
	cobra.EnablePrefixMatching = true

	root := &cobra.Command{
		Use:           "rig",
		Short:         "Convention-first dev launcher across .NET, Node, and Go",
		Long:          "rig wraps the everyday dev loop — run, build, test, format, lint, typecheck —\nwith project discovery, so the same command works in any ecosystem.",
		SilenceUsage:  true,
		SilenceErrors: false,
		// Fold .rig.json's quiet (global ~/.rig.json layered under the repo's)
		// into the flag before any verb runs, so the config sets the default
		// and an explicit --quiet always wins.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			if cfg, err := config.LoadMerged(resolveRoot(cwd)); err == nil && cfg.IsQuiet() {
				quiet = true
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVarP(&dryRun, "dry-run", "n", false, "print the command instead of running it")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress the → command echo")
	root.PersistentFlags().BoolVar(&noEnv, "no-env", false, "skip .env/.env.local loading for this run")
	root.PersistentFlags().StringVar(&rootFlag, "root", "", "override the working root (skip walk-up discovery)")

	root.AddCommand(
		// Dev loop (workspace-aware: [project] scopes; --all runs across the
		// workspace in dependency order, with --filter).
		devVerbCmd("build", "Build the project", true),
		devVerbCmd("test", "Run the tests", true),
		devVerbCmd("run", "Run the project", false, "dev"),
		devVerbCmd("format", "Format the code", true, "fmt"),
		devVerbCmd("lint", "Lint the code", true),
		devVerbCmd("typecheck", "Type-check the code", true, "check"),
		devVerbCmd("clean", "Remove build outputs", true),
		devVerbCmd("rebuild", "Clean then build", false, "rb"),
		// Dependencies & maintenance.
		verbCmd("install", "Install/restore dependencies", "restore"),
		verbCmd("ci", "Frozen/clean install"),
		verbCmd("add", "Add a dependency"),
		newUninstallCmd(),
		newOutdatedCmd(),
		newUpgradeCmd(),
		verbCmd("global", "Install a global tool", "g"),
		newDlxCmd(),
		newWatchCmd(),
		newWorktreeCmd(),
		newRigInitCmd(),
		newInfoCmd(),
		newConfigCmd(),
		newUICmd(),
		newSelfUpdateCmd(),
	)
	root.AddCommand(extraCmds()...)

	cwd, _ := os.Getwd()
	repoRoot := detect.Root(cwd)
	// Surface custom commands from the nearest .rig.json (with the user-wide
	// ~/.rig.json layered underneath, so personal verbs work everywhere), plus
	// (for Node) every package.json script that isn't already a built-in verb.
	if cfg, err := config.LoadMerged(repoRoot); err == nil {
		root.AddCommand(customCmds(cfg)...)
	}
	root.AddCommand(scriptCmds(repoRoot)...)
	// Go script-dir verbs are added last and never override an already-surfaced
	// command (a custom command or a package.json script keeps its name).
	taken := map[string]bool{}
	for _, c := range root.Commands() {
		taken[c.Name()] = true
	}
	for _, c := range goScriptCmds(repoRoot) {
		if !taken[c.Name()] {
			root.AddCommand(c)
		}
	}

	// Pre-parse pipeline, mirroring the .NET rig: a leading `watch`/`w` modifier
	// expands to a --watch flag on the target verb, then an unambiguous verb
	// prefix resolves (`rig cove` → coverage). Tokens after `--` are forwarded
	// verbatim and never rewritten.
	args := os.Args[1:]
	head, tail := args, []string(nil)
	if sep := slices.Index(args, "--"); sep >= 0 {
		head, tail = args[:sep:sep], args[sep:]
	}
	verbs := make([]string, 0, len(root.Commands()))
	for _, c := range root.Commands() {
		// Script-directory verbs are exact-match only: keep them out of prefix
		// resolution so a typo can't expand into repo-provided code.
		if c.Annotations[scriptVerbAnnotation] != "" {
			continue
		}
		verbs = append(verbs, c.Name())
	}
	head = resolvePrefix(expandWatch(head), verbs)
	// Go's watch mode is the `watch` subcommand rather than per-verb --watch
	// flags; fold the expanded flag back onto it.
	if n := len(head); n > 0 && head[n-1] == "--watch" {
		head = append([]string{"watch"}, head[:n-1]...)
	}
	root.SetArgs(append(head, tail...))

	// Surface the ldflags-stamped build version in `rig --version` (fang owns
	// the flag); a source build keeps fang's "built from source" default.
	opts := []fang.Option{fang.WithColorSchemeFunc(rigColorScheme)}
	if version != "dev" {
		opts = append(opts, fang.WithVersion(version))
	}
	return fang.Execute(ctx, root, opts...)
}

// resolvePrefix rewrites an unambiguous *prefix* of a verb name to the full
// name before the parser sees it (`rig cove` → `rig coverage`), matching the
// .NET rig's PrefixResolver.Resolve. Exact names, option-looking tokens, and
// ambiguous/unknown tokens pass through for the parser to handle.
func resolvePrefix(args []string, verbs []string) []string {
	if len(args) == 0 {
		return args
	}
	token := args[0]
	if token == "" || token[0] == '-' {
		return args
	}
	for _, v := range verbs {
		if strings.EqualFold(v, token) {
			return args // exact name
		}
	}
	matched := map[string]bool{}
	match := ""
	for _, v := range verbs {
		if len(v) >= len(token) && strings.EqualFold(v[:len(token)], token) {
			matched[strings.ToLower(v)] = true
			match = v
		}
	}
	if len(matched) != 1 {
		return args // ambiguous / none → the parser handles it (or an alias does)
	}
	rewritten := make([]string, len(args))
	copy(rewritten, args)
	rewritten[0] = match
	return rewritten
}

// resolvePrimary picks the primary ecosystem for the repo at root: .rig.json's
// "ecosystem" wins if set, otherwise the nearest manifest walking up from cwd.
// When the nearest level is ambiguous (several ecosystems coexist and no
// .rig.json pins one) it offers an interactive picker on a TTY (caching the
// choice per root so it asks once per process, and offering to persist it);
// off a TTY it returns the "set ecosystem" error so the dev verbs and `ui` stop
// with a clear message instead of guessing. A "nothing found" result errors too.
func resolvePrimary(cwd, root string) (eco string, err error) {
	if cfg, cerr := config.LoadMerged(root); cerr == nil && cfg.Ecosystem != "" {
		return cfg.Ecosystem, nil
	}
	if e, ok := pickedEcosystem[root]; ok {
		return e, nil
	}
	id, candidates := detect.NearestEcosystem(cwd)
	if len(candidates) > 0 {
		if chosen, ok := pickPrimaryEcosystem(root, candidates); ok {
			pickedEcosystem[root] = chosen
			return chosen, nil
		}
		return "", fmt.Errorf(
			"multiple ecosystems found here (%s) — set \"ecosystem\" in %s to choose one",
			strings.Join(candidates, ", "), config.FileName)
	}
	if id == "" {
		return "", fmt.Errorf("no recognized ecosystem (.NET/Node/Go/Cargo) found at %s", root)
	}
	return id, nil
}

func verbCmd(verb, short string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     verb,
		Short:   short,
		Aliases: aliases,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			argv, ok := detect.CommandFor(eco, verb, root)
			if !ok {
				return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", verb, eco)
			}
			argv = append(argv, args...)
			return runCommand(cmd, root, argv)
		},
	}
}

// runCommand runs an ecosystem verb's argv in dir, echoing it first unless quiet.
func runCommand(cmd *cobra.Command, dir string, argv []string) error {
	echo(cmd, strings.Join(argv, " "))
	if dryRun {
		return nil
	}
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = commandEnv(dir)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Stdin = os.Stdin
	return c.Run()
}

// resolveRoot resolves the working root for a command: the explicit `--root`
// override when given (skipping walk-up discovery), else the nearest discovered
// root walking up from cwd.
func resolveRoot(cwd string) string {
	if rootFlag != "" {
		if abs, err := filepath.Abs(rootFlag); err == nil {
			return abs
		}
		return rootFlag
	}
	return detect.Root(cwd)
}

// commandEnv builds the spawned-process environment with the rig layering
// (low to high): .env/.env.local files, ambient process env, `.rig.json` env
// (the user-wide ~/.rig.json merged under the repo's, repo winning per key),
// and finally the active env presets. `--no-env` drops the file layer.
// Returns nil (inherit) when no layer contributes anything.
func commandEnv(root string) []string {
	var fileEnv map[string]string
	if !noEnv {
		fileEnv, _ = envstack.Load(root)
	}
	cfg, _ := config.LoadMerged(root)
	if len(fileEnv) == 0 && len(cfg.Env) == 0 && len(presetEnv) == 0 {
		return nil
	}
	return envstack.Environ(envstack.Merge(fileEnv, envstack.Ambient(), cfg.Env, presetEnv))
}

// echo prints the `→ command` line unless --quiet (or .rig.json quiet) is set.
func echo(cmd *cobra.Command, line string) {
	if quiet {
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("→ "+line))
}
