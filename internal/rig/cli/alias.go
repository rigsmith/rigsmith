package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// `rig alias` installs short shell aliases for the rig verbs you type most, in
// a marked block spliced idempotently into your shell startup file — the same
// mechanism `rig setup` uses, but a separate block (its own markers) so the two
// are managed independently: re-running `rig setup` never touches your aliases,
// and `rig alias remove` never touches your completion/cd wrapper.
//
// Aliases claim names in your shell's global namespace, so they're opt-in (you
// run `rig alias install`, they aren't part of the default `rig setup`) and
// deliberately non-colliding: "rrm" for uninstall rather than "run", which would
// shadow the ubiquitous run command. The candidate set is fixed, but which of it
// you install is your choice — a terminal gets an interactive checklist, and
// --only / --all pick without prompting (see resolveAliasSelection).

// rigAlias is one installed alias: the short name, the rig verb it expands to,
// and a one-line description for `rig alias list`. cd marks the navigation alias
// (rcd), which renders as a self-contained cd function rather than a passthrough
// (see aliasCdFunc).
type rigAlias struct {
	name string
	verb string
	desc string
	cd   bool
}

// rigAliases is the fixed set `rig alias` installs, ordered inner-loop first.
// All target the base `rig` name (integrationBase); the plain passthroughs flow
// through the `rig` wrapper function `rig setup` installs (when present), while
// rcd calls the binary directly so it works with or without that wrapper.
var rigAliases = []rigAlias{
	{"rr", "run", "Run the project", false},
	{"rb", "build", "Build the project", false},
	{"rt", "test", "Run the tests", false},
	{"rf", "format", "Format the code", false},
	{"rl", "lint", "Lint the code", false},
	{"rcd", "cd", "Jump to a project (cds the shell)", true},
	{"ri", "install", "Install/restore dependencies", false},
	{"rup", "upgrade", "Upgrade dependencies", false},
	{"rrm", "uninstall", "Uninstall packages", false},
	{"rk", "kill", "Kill dev processes", false},
	{"rw", "watch", "Watch a verb — e.g. `rw r`", false},
}

// Markers bracketing the managed alias block. Distinct from setup's
// "shell integration" markers so the two blocks coexist and are edited
// independently in one rc file.
func aliasMarkerBegin() string { return "# >>> rig aliases >>>" }
func aliasMarkerEnd() string   { return "# <<< rig aliases <<<" }

// newAliasCmd builds the `alias` group: install / remove / list. Bare `rig
// alias` lists the set (safe and informative, nothing is written).
func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage short shell aliases for common rig verbs (rr, rb, rt, rcd, …)",
		Long: strings.TrimSpace(`
Install short shell aliases for the rig verbs you reach for most — rr (run),
rb (build), rt (test), rcd (cd), and more. On a terminal "rig alias install"
shows a checklist so you pick exactly which ones you want; --only and --all
choose without the prompt.

They're written to your shell startup file in their own marked block (separate
from "rig setup"), so re-running setup leaves them alone and "rig alias remove"
takes them back out cleanly. The shell is taken from the argument, else $SHELL;
supported: zsh, bash, fish, powershell (alias: pwsh).

Aliases are opt-in — they claim names in your shell, so they aren't part of the
default "rig setup". Bare "rig alias" just lists the set without writing.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAliasList(cmd)
		},
	}
	cmd.AddCommand(newAliasInstallCmd(), newAliasRemoveCmd(), newAliasListCmd())
	return cmd
}

func newAliasInstallCmd() *cobra.Command {
	var printOnly bool
	var all bool
	var only []string
	cmd := &cobra.Command{
		Use:   "install [shell]",
		Short: "Add the alias block to your shell startup file",
		Long: strings.TrimSpace(`
Add the alias block to your shell startup file.

By default, on a terminal, "rig alias install" shows a checklist so you pick
exactly which aliases you want (all pre-checked — uncheck the ones you'll skip).
Off a terminal it installs the full set. --only names a subset directly, and
--all installs everything without the prompt:

  rig alias install --only rb,rt,rcd    # just these
  rig alias install --all               # the whole set, no prompt`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: setupShellCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveSetupShell(args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Read the current rc + alias block up front (not for --print, which
			// never touches the file). The checklist pre-checks whatever is
			// already installed, so re-running is a true edit: unchecking an
			// alias drops it, checking a new one adds it. First install (no
			// block) pre-checks the full set.
			var rcPath, existing string
			preselected := rigAliases
			if !printOnly {
				if rcPath, err = rcFileFor(shell); err != nil {
					return err
				}
				if data, rerr := os.ReadFile(rcPath); rerr == nil {
					existing = string(data)
				} else if !errors.Is(rerr, fs.ErrNotExist) {
					return rerr
				}
				if block, ok := extractBlock(existing, aliasMarkerBegin(), aliasMarkerEnd()); ok {
					if inst := installedAliases(shell, block); len(inst) > 0 {
						preselected = inst
					}
				}
			}

			selection, cancelled, clearAll, err := resolveAliasSelection(only, all, printOnly, preselected)
			if err != nil {
				return err
			}
			if cancelled {
				fmt.Fprintln(out, "Cancelled — nothing written.")
				return nil
			}

			// Deselecting everything in the checklist means "remove them all".
			if clearAll {
				updated, changed := removeBlock(existing, aliasMarkerBegin(), aliasMarkerEnd())
				if !changed {
					fmt.Fprintf(out, "no rig aliases in %s — nothing to do.\n", rcPath)
					return nil
				}
				if dryRun {
					fmt.Fprintln(out, dimStyle.Render("→ would remove the rig aliases block from "+rcPath))
					return nil
				}
				if err := os.WriteFile(rcPath, []byte(updated), 0o644); err != nil {
					return fmt.Errorf("couldn't update %s: %w", rcPath, err)
				}
				fmt.Fprintf(out, "Removed rig aliases from %s (all deselected)\n", rcPath)
				fmt.Fprintln(out, "Restart your shell for it to take effect.")
				return nil
			}
			if len(selection) == 0 {
				fmt.Fprintln(out, "No aliases selected — nothing to do.")
				return nil
			}

			snippet := aliasSnippetFor(shell, selection)
			if printOnly {
				fmt.Fprintln(out, snippet)
				return nil
			}
			if dryRun {
				fmt.Fprintln(out, dimStyle.Render("→ would write "+rcPath+":"))
				fmt.Fprintln(out, snippet)
				return nil
			}
			changed, err := installBlock(rcPath, snippet, aliasMarkerBegin(), aliasMarkerEnd())
			if err != nil {
				return fmt.Errorf("couldn't update %s: %w", rcPath, err)
			}
			if !changed {
				fmt.Fprintf(out, "rig aliases already installed in %s — nothing to do.\n", rcPath)
				return nil
			}
			fmt.Fprintf(out, "Installed rig aliases (%s) in %s\n", aliasNamesOf(selection), rcPath)
			fmt.Fprintf(out, "Restart your shell or run: source %s\n", rcPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the snippet instead of writing the rc file")
	cmd.Flags().BoolVar(&all, "all", false, "install every alias without the interactive prompt")
	cmd.Flags().StringSliceVar(&only, "only", nil, "install only these aliases (comma-separated, e.g. rb,rt,rcd)")
	cmd.MarkFlagsMutuallyExclusive("all", "only")
	return cmd
}

// resolveAliasSelection decides which aliases to install. --only names a subset
// explicitly; --all takes everything. With neither, an interactive terminal gets
// the checklist (unless printOnly, which never prompts) pre-checked with
// preselected, while everything else — pipes, CI, `rig setup --aliases` — falls
// back to the full set. cancelled is true when the user escapes the checklist;
// clearAll is true when they confirm it with nothing checked (remove them all).
func resolveAliasSelection(only []string, all, printOnly bool, preselected []rigAlias) (sel []rigAlias, cancelled, clearAll bool, err error) {
	if len(only) > 0 {
		sel, err = aliasesByName(only)
		return sel, false, false, err
	}
	if all {
		return rigAliases, false, false, nil
	}
	if !printOnly && stdinStdoutTTY() {
		chosen, ok := pickAliases(preselected)
		if !ok {
			return nil, true, false, nil
		}
		if len(chosen) == 0 {
			return nil, false, true, nil
		}
		return chosen, false, false, nil
	}
	return rigAliases, false, false, nil
}

// pickAliases shows the candidates with preselected pre-checked; the user
// toggles the set, then confirms. Returns the selected aliases (in canonical
// order), or ok=false on esc/ctrl+c.
func pickAliases(preselected []rigAlias) (sel []rigAlias, ok bool) {
	pre := map[string]bool{}
	for _, a := range preselected {
		pre[a.name] = true
	}
	var selected []string
	opts := make([]huh.Option[string], 0, len(rigAliases))
	for _, a := range rigAliases {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-4s %s %s", a.name, integrationBase, a.verb), a.name).Selected(pre[a.name]))
	}
	ms := huh.NewMultiSelect[string]().
		Title("Which aliases? (space toggles · enter confirms · esc cancels)").
		Options(opts...).
		Value(&selected)
	if err := runHuhMultiSelect(ms); err != nil {
		return nil, false
	}
	chosen, _ := aliasesByName(selected)
	return chosen, true
}

// installedAliases reports which candidate aliases the given block already
// defines, by matching each one's rendered line — so the checklist can pre-check
// exactly what's there. Order follows canonical rigAliases.
func installedAliases(shell, block string) []rigAlias {
	var out []rigAlias
	for _, a := range rigAliases {
		if strings.Contains(block, aliasLine(shell, a)) {
			out = append(out, a)
		}
	}
	return out
}

// aliasesByName resolves alias names to their definitions, preserving the
// canonical rigAliases order (not the argument order) so the rendered block is
// stable. It errors on any unknown name.
func aliasesByName(names []string) ([]rigAlias, error) {
	want := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var out []rigAlias
	for _, a := range rigAliases {
		if want[a.name] {
			out = append(out, a)
			delete(want, a.name)
		}
	}
	if len(want) > 0 {
		unknown := make([]string, 0, len(want))
		for n := range want {
			unknown = append(unknown, n)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown alias %s — available: %s", strings.Join(unknown, ", "), aliasNames())
	}
	return out, nil
}

func newAliasRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "remove [shell]",
		Aliases:           []string{"uninstall"},
		Short:             "Remove the alias block from your shell startup file",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: setupShellCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveSetupShell(args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			rcPath, err := rcFileFor(shell)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(rcPath)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			updated, changed := removeBlock(string(data), aliasMarkerBegin(), aliasMarkerEnd())
			if !changed {
				fmt.Fprintf(out, "no rig aliases in %s — nothing to do.\n", rcPath)
				return nil
			}
			if dryRun {
				fmt.Fprintln(out, dimStyle.Render("→ would remove the rig aliases block from "+rcPath))
				return nil
			}
			if err := os.WriteFile(rcPath, []byte(updated), 0o644); err != nil {
				return fmt.Errorf("couldn't update %s: %w", rcPath, err)
			}
			fmt.Fprintf(out, "Removed rig aliases from %s\n", rcPath)
			fmt.Fprintln(out, "Restart your shell for it to take effect.")
			return nil
		},
	}
	return cmd
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show the aliases rig installs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAliasList(cmd)
		},
	}
}

func runAliasList(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	for _, a := range rigAliases {
		fmt.Fprintf(out, "  %-4s %s\n", a.name, dimStyle.Render(integrationBase+" "+a.verb+"  — "+a.desc))
	}
	return nil
}

// aliasNames renders every candidate alias name for a one-line summary, e.g.
// "rr, rb, …". aliasNamesOf does the same for a chosen subset.
func aliasNames() string { return aliasNamesOf(rigAliases) }

func aliasNamesOf(aliases []rigAlias) string {
	names := make([]string, len(aliases))
	for i, a := range aliases {
		names[i] = a.name
	}
	return strings.Join(names, ", ")
}

// aliasSnippet renders the block for the full candidate set; aliasSnippetFor
// renders a chosen subset. `rig setup --aliases` and the non-interactive default
// use the full set.
func aliasSnippet(shell string) string { return aliasSnippetFor(shell, rigAliases) }

// aliasSnippetFor renders the marked rc-file block for the shell (no trailing
// newline — installBlock owns the framing) for the given aliases. The header
// uses "#" comments, which every supported shell (including PowerShell)
// understands.
func aliasSnippetFor(shell string, aliases []rigAlias) string {
	begin, end := aliasMarkerBegin(), aliasMarkerEnd()
	lines := []string{
		"# Installed by 'rig alias install' — safe to re-run; replaced in place.",
		"# Short aliases for common rig verbs. Remove with 'rig alias remove'.",
	}
	for _, a := range aliases {
		lines = append(lines, aliasLine(shell, a))
	}
	return begin + "\n" + strings.Join(lines, "\n") + "\n" + end
}

// aliasLine renders one alias for the given shell. POSIX shells and fish use
// their alias builtins; PowerShell's Set-Alias can't carry arguments, so the
// alias is a thin function that forwards @args. The cd alias (rcd) is special —
// see aliasCdFunc.
func aliasLine(shell string, a rigAlias) string {
	if a.cd {
		return aliasCdFunc(shell, a.name)
	}
	switch shell {
	case "fish":
		return fmt.Sprintf("alias %s '%s %s'", a.name, integrationBase, a.verb)
	case "powershell":
		return fmt.Sprintf("function %s { %s %s @args }", a.name, integrationBase, a.verb)
	default: // zsh, bash
		return fmt.Sprintf("alias %s='%s %s'", a.name, integrationBase, a.verb)
	}
}

// aliasCdFunc renders rcd, a self-contained take on the `rig cd` wrapper. `rig
// cd` only prints the target dir (a subprocess can't cd its parent shell), so
// the alias must capture that output and cd itself. It calls the rig binary
// directly — `command rig` / Get-Command Application — not the `rig` shell
// function `rig setup` may install, so rcd works whether or not that wrapper is
// present, and never double-handles the cd.
func aliasCdFunc(shell, name string) string {
	switch shell {
	case "fish":
		return fmt.Sprintf(`function %s
    set -l __rig_dir (command %s cd $argv)
    and test -n "$__rig_dir"
    and builtin cd -- $__rig_dir
end`, name, integrationBase)
	case "powershell":
		return fmt.Sprintf(`function %s {
    $bin = (Get-Command -Name %s -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
    if (-not $bin) { return }
    $dir = & $bin cd @args | Select-Object -Last 1
    if ($LASTEXITCODE -eq 0 -and $dir) { Set-Location -LiteralPath $dir }
}`, name, integrationBase)
	default: // zsh, bash
		return fmt.Sprintf(`%s() {
  local __rig_dir
  __rig_dir="$(command %s cd "$@")" && [ -n "$__rig_dir" ] && builtin cd -- "$__rig_dir"
}`, name, integrationBase)
	}
}

// resolveSetupShell picks the target shell from an optional [shell] arg, else
// $SHELL, normalizing the pwsh alias and rejecting unsupported shells. Shared by
// the `rig alias` subcommands.
func resolveSetupShell(args []string) (string, error) {
	shell := ""
	if len(args) == 1 {
		shell = strings.ToLower(strings.TrimSpace(args[0]))
	}
	if shell == "pwsh" {
		shell = "powershell"
	}
	if shell == "" {
		shell = shellFromEnv()
	}
	if !isSetupShell(shell) {
		return "", fmt.Errorf("unknown shell %q — supported: %s", shell, strings.Join(setupShells, ", "))
	}
	return shell, nil
}
