package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/shellrun"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// directVerbs are the built-in verbs whose command is exactly "what the
// ecosystem maps for this verb, plus your args, at the repo root" — the ones
// `explain` can describe without running anything.
//
// The rest are deliberately absent. `coverage` appends report flags and
// per-ecosystem augmentation, `rebuild` sequences clean → build, `publish`
// assembles its own argv, `upgrade`/`outdated` branch on what the ecosystem can
// report. For those, resolution happens partly at run time, and a plausible
// guess printed here is exactly the failure `explain` exists to prevent — so it
// says so and points at `--dry-run`, which goes through the real path.
var directVerbs = map[string]bool{
	"build": true, "test": true, "run": true, "format": true, "lint": true,
	"typecheck": true, "clean": true, "install": true, "ci": true, "add": true,
	"global": true, "dlx": true,
}

// newExplainCmd builds `rig explain [verb]` — the answer to "what does this
// verb actually run", without running it.
//
// It exists because a custom command can be silently wrong: a `.rig.json` entry
// is valid JSON holding a valid shell line that quietly does the wrong thing,
// and neither the config nor the exit code shows it. Nothing validates that for
// you, but a resolved command you can read takes seconds to check.
func newExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "explain [verb] [args...]",
		Short:   "Show what a verb resolves to, without running it",
		Aliases: []string{"why"},
		Long: "Show what a verb resolves to: the command line after OS selection, the\n" +
			"directory it runs in, the environment rig contributes, and whether the verb\n" +
			"came from .rig.json or from an ecosystem convention.\n\n" +
			"Bare, it lists every verb this repo resolves. Nothing is executed either way.",
		Example: "# what does this custom command actually run?\n" +
			"rig explain markers\n\n" +
			"# every verb this repo resolves, in one list\n" +
			"rig explain",
		ValidArgsFunction: explainCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			cfg, _ := config.LoadMerged(root)
			out := cmd.OutOrStdout()

			if len(args) == 0 {
				return explainAll(cmd, out, root, cfg)
			}
			p, err := explainPlan(cmd, cwd, root, cfg, args[0], args[1:])
			if err != nil {
				return err
			}
			printPlan(out, p)
			return nil
		},
	}
	// Everything after the verb belongs to the verb: `rig explain markers
	// --color` asks what `rig markers --color` resolves to, so the flag has to
	// reach the explained command instead of being parsed as one of explain's.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// explainPlan resolves one verb to its plan, in the order the command tree is
// built: rig's own verbs first (they are why a same-named custom command never
// runs), then custom commands, package.json scripts and script directories.
func explainPlan(cmd *cobra.Command, cwd, root string, cfg config.Config, name string, args []string) (commandPlan, error) {
	verb := canonicalVerb(cmd, name)

	if isBuiltinVerb[verb] {
		if !directVerbs[verb] {
			return commandPlan{}, fmt.Errorf(
				"part of `rig %s`'s command is decided while it runs, so nothing printed here would be guaranteed to match it — run `rig %s --dry-run` for the exact command",
				verb, verb)
		}
		if len(args) > 0 {
			return commandPlan{}, fmt.Errorf(
				"an argument to `rig %s` selects a project or a filter, which is resolved while the verb runs — explain covers the bare verb, so for one invocation run `rig %s %s --dry-run`",
				verb, verb, strings.Join(args, " "))
		}
		eco, err := resolvePrimary(cwd, root)
		if err != nil {
			return commandPlan{}, err
		}
		p, ok := ecosystemPlan(eco, root, verb, args)
		if !ok {
			return commandPlan{}, fmt.Errorf("verb %q has no mapping for ecosystem %q yet", verb, eco)
		}
		p.notes = append(p.notes, shadowNote(cfg, verb)...)
		return p, nil
	}

	for _, e := range discoverScripts(root, cfg) {
		if e.name != name && e.name != verb {
			continue
		}
		p, err := e.plan(args)
		if err != nil {
			return commandPlan{}, err
		}
		return p, nil
	}
	return commandPlan{}, fmt.Errorf("no verb named %q here — run `rig explain` to list what this repo resolves", name)
}

// canonicalVerb maps what the user typed to the verb it runs, through the live
// command tree — so an alias (`fmt`, `rb`, `x`) and a prefix explain the same
// thing they run. An unknown name is returned unchanged, for the caller's error.
func canonicalVerb(cmd *cobra.Command, name string) string {
	root := cmd.Root()
	if target, _, err := root.Find([]string{name}); err == nil && target != root {
		return target.Name()
	}
	return name
}

// shadowNote reports a `commands` entry that this built-in verb shadows —
// standing where the user is most likely to be looking for it, having just
// asked why `rig build` doesn't do what their config says.
func shadowNote(cfg config.Config, verb string) []string {
	if cfg.Commands[verb] == nil {
		return nil
	}
	return []string{shadowWarning(verb, cfg.Path)}
}

// printPlan renders one resolved verb.
func printPlan(w io.Writer, p commandPlan) {
	fmt.Fprintln(w, headerStyle.Render("Verb"))
	fmt.Fprintf(w, "  name:    %s\n", p.verb)
	fmt.Fprintf(w, "  source:  %s %s\n\n", p.origin, dimStyle.Render("· "+p.detail))

	fmt.Fprintln(w, headerStyle.Render("Command"))
	switch p.kind {
	case planScript:
		fmt.Fprintf(w, "  runs:    %s\n", dimStyle.Render("a Tengo script"))
		for _, line := range strings.Split(strings.TrimRight(p.code, "\n"), "\n") {
			fmt.Fprintf(w, "           %s\n", line)
		}
		fmt.Fprintf(w, "  sh():    %s\n", shellDescription(p.shell))
	case planShell:
		fmt.Fprintf(w, "  runs:    %s\n", p.line)
		fmt.Fprintf(w, "  shell:   %s\n", shellDescription(p.shell))
	default:
		fmt.Fprintf(w, "  runs:    %s\n", p.line)
		fmt.Fprintf(w, "  exec:    %s\n", dimStyle.Render("directly, no shell"))
	}
	fmt.Fprintf(w, "  dir:     %s\n\n", p.dir)

	fmt.Fprintln(w, headerStyle.Render("Environment"))
	vars := planEnv(p.layers)
	width := 0
	for _, e := range vars {
		width = max(width, runeLen(e.key+"="+e.value))
	}
	for _, e := range vars {
		from := e.from
		if e.overriden {
			from += ", overridden by the ambient environment"
		}
		fmt.Fprintf(w, "  %s  %s\n", padRight(e.key+"="+e.value, width), dimStyle.Render("· "+from))
	}
	if len(vars) == 0 {
		fmt.Fprintln(w, dimStyle.Render("  (rig adds nothing)"))
	}
	fmt.Fprintln(w, dimStyle.Render("  the rest is inherited from the current environment"))

	if len(p.notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range p.notes {
			fmt.Fprintln(w, warnStyle.Render("  ! "+n))
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, dimStyle.Render(fmt.Sprintf("  nothing ran — `rig %s` runs it", p.verb)))
}

// shellDescription names how a shell-string command is executed, since which
// shell it is decides what syntax works.
func shellDescription(mode string) string {
	if mode == shellrun.ShellSystem {
		return "system " + dimStyle.Render("· the OS shell (sh -c / cmd.exe)")
	}
	return "portable " + dimStyle.Render("· rig's in-process POSIX shell, same on every OS")
}

// explainAll lists every verb this repo resolves with the command each becomes:
// the ecosystem's dev loop, then the custom commands and surfaced scripts. It's
// the overview a bare `rig explain` gives before you narrow to one verb.
func explainAll(cmd *cobra.Command, w io.Writer, root string, cfg config.Config) error {
	cwd, _ := os.Getwd()
	eco, ecoErr := resolvePrimary(cwd, root)
	if ecoErr == nil {
		detail := eco
		if eco == detect.Node {
			detail += " · " + string(detect.DetectNodePM(root))
		}
		fmt.Fprintf(w, "%s %s\n", headerStyle.Render("Ecosystem verbs"), dimStyle.Render(detail))
		var rows [][2]string
		for _, verb := range sortedDirectVerbs() {
			if p, ok := ecosystemPlan(eco, root, verb, nil); ok {
				rows = append(rows, [2]string{verb, p.line})
			}
		}
		printExplainRows(w, rows)
	}

	scripts := discoverScripts(root, cfg)
	for _, group := range []struct {
		title string
		eco   string
	}{
		{"Custom commands", "custom"},
		{"package.json scripts", "node"},
		{"Script directories", "go"},
	} {
		var rows []scriptEntry
		for _, e := range scripts {
			if e.eco == group.eco {
				rows = append(rows, e)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintln(w, headerStyle.Render(group.title))
		lines := make([][2]string, 0, len(rows))
		for _, e := range rows {
			lines = append(lines, [2]string{e.name, explainOneLine(e)})
		}
		printExplainRows(w, lines)
	}

	for _, name := range shadowedCommands(cfg) {
		fmt.Fprintln(w, warnStyle.Render("  ! "+shadowWarning(name, cfg.Path)))
	}
	if ecoErr != nil && len(scripts) == 0 {
		return ecoErr
	}
	fmt.Fprintln(w, dimStyle.Render("  `rig explain <verb>` for one verb's directory, environment and source"))
	return nil
}

// printExplainRows prints one section of the overview: verb, then the command
// it resolves to, in a column wide enough for the section's longest name.
func printExplainRows(w io.Writer, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, runeLen(r[0]))
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %s\n", padRight(r[0], width), dimStyle.Render(r[1]))
	}
	fmt.Fprintln(w)
}

// explainOneLine is a script entry's resolved command on one line, or the
// resolution error in its place — a command that can't resolve is exactly what
// the list should show, not something to omit.
func explainOneLine(e scriptEntry) string {
	p, err := e.plan(nil)
	if err != nil {
		return err.Error()
	}
	return p.line
}

// sortedDirectVerbs lists the explainable built-ins in dev-loop order, so the
// overview reads the way the loop runs rather than alphabetically.
func sortedDirectVerbs() []string {
	order := []string{"build", "test", "run", "format", "lint", "typecheck", "clean", "install", "ci", "add", "global", "dlx"}
	var out []string
	for _, v := range order {
		if directVerbs[v] {
			out = append(out, v)
		}
	}
	return out
}

// explainCompletion completes the verb arg with what `explain` can resolve:
// the direct built-ins plus every custom command and surfaced script.
func explainCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	cfg, _ := config.LoadMerged(root)
	names := sortedDirectVerbs()
	for _, e := range discoverScripts(root, cfg) {
		names = append(names, e.name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}
