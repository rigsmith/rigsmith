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
	"sort"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

var (
	dryRun bool
	quiet  bool
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
		// Fold .rig.json's quiet into the flag before any verb runs, so the
		// config sets the default and an explicit --quiet always wins.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			if cfg, err := config.Load(detect.Root(cwd)); err == nil && cfg.Quiet {
				quiet = true
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVarP(&dryRun, "dry-run", "n", false, "print the command instead of running it")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress the → command echo")

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
		verbCmd("uninstall", "Remove a dependency", "remove", "rm"),
		verbCmd("outdated", "List outdated dependencies", "od"),
		verbCmd("upgrade", "Upgrade dependencies"),
		verbCmd("global", "Install a global tool", "g"),
		verbCmd("dlx", "Run a tool once without installing", "x"),
		newWatchCmd(),
		newRigInitCmd(),
		newInfoCmd(),
		newUICmd(),
	)
	root.AddCommand(extraCmds()...)

	cwd, _ := os.Getwd()
	repoRoot := detect.Root(cwd)
	// Surface per-repo custom commands from the nearest .rig.json, plus (for Node)
	// every package.json script that isn't already a built-in verb.
	if cfg, err := config.Load(repoRoot); err == nil {
		root.AddCommand(customCmds(cfg)...)
	}
	root.AddCommand(scriptCmds(repoRoot)...)

	return fang.Execute(ctx, root)
}

// resolvePrimary picks the primary ecosystem for the repo at root: .rig.json's
// "ecosystem" wins if set, otherwise the nearest manifest walking up from cwd.
// It returns a non-nil error when the nearest level is ambiguous (several
// ecosystems coexist and no .rig.json pins one) or when nothing was found, so
// the dev verbs and `ui` can stop with a clear message instead of guessing.
func resolvePrimary(cwd, root string) (eco string, err error) {
	if cfg, cerr := config.Load(root); cerr == nil && cfg.Ecosystem != "" {
		return cfg.Ecosystem, nil
	}
	id, candidates := detect.NearestEcosystem(cwd)
	if len(candidates) > 0 {
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
			root := detect.Root(cwd)
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

// customCmds turns each .rig.json "commands" entry into a rig subcommand that
// runs the shell string via `sh -c`. Names that collide with a built-in verb are
// skipped so the dev loop always wins.
func customCmds(cfg config.Config) []*cobra.Command {
	if len(cfg.Commands) == 0 {
		return nil
	}
	builtin := isBuiltinVerb
	names := make([]string, 0, len(cfg.Commands))
	for name := range cfg.Commands {
		names = append(names, name)
	}
	sort.Strings(names)

	var cmds []*cobra.Command
	for _, name := range names {
		if builtin[name] {
			continue
		}
		script := cfg.Commands[name]
		cmds = append(cmds, &cobra.Command{
			Use:   name,
			Short: "Custom command: " + script,
			// Let unknown flags fall through to the script while rig's own
			// --dry-run/--quiet still bind.
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				cwd, _ := os.Getwd()
				root := detect.Root(cwd)
				line := script
				if len(args) > 0 {
					line = line + " " + strings.Join(args, " ")
				}
				return runShell(cmd, root, line)
			},
		})
	}
	return cmds
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

// runShell runs a custom command's shell string in dir via `sh -c`.
func runShell(cmd *cobra.Command, dir, line string) error {
	echo(cmd, line)
	if dryRun {
		return nil
	}
	c := exec.CommandContext(cmd.Context(), "sh", "-c", line)
	c.Dir = dir
	c.Env = commandEnv(dir)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Stdin = os.Stdin
	return c.Run()
}

// commandEnv returns the ambient environment plus any `.rig.json` `env` entries
// (config env overrides ambient). Returns nil (inherit) when there's no config env.
func commandEnv(root string) []string {
	cfg, err := config.Load(root)
	if err != nil || len(cfg.Env) == 0 {
		return nil
	}
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// echo prints the `→ command` line unless --quiet (or .rig.json quiet) is set.
func echo(cmd *cobra.Command, line string) {
	if quiet {
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("→ "+line))
}
