package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// `rig alias` installs short shell aliases for the rig verbs you type most, in
// a marked block spliced idempotently into your shell startup file — the same
// mechanism `rig setup` uses, but a separate block (its own markers) so the two
// are managed independently: re-running `rig setup` never touches your aliases,
// and `rig alias remove` never touches your completion/cd wrapper.
//
// The set is intentionally fixed and small. Aliases claim names in your shell's
// global namespace, so they're opt-in (you run `rig alias install`, they aren't
// part of the default `rig setup`) and deliberately non-colliding: "rrm" for
// uninstall rather than "run", which would shadow the ubiquitous run command.

// rigAlias is one installed alias: the short name, the rig verb it expands to,
// and a one-line description for `rig alias list`.
type rigAlias struct {
	name string
	verb string
	desc string
}

// rigAliases is the fixed set `rig alias` installs. All target the base `rig`
// name (integrationBase), so when the `rig` wrapper function from `rig setup` is
// present the alias flows through it just like a bare `rig` call.
var rigAliases = []rigAlias{
	{"rr", "run", "Run the project"},
	{"ri", "install", "Install/restore dependencies"},
	{"rup", "upgrade", "Upgrade dependencies"},
	{"rrm", "uninstall", "Uninstall packages"},
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
		Short: "Manage short shell aliases for common rig verbs (rr, ri, rup, rrm)",
		Long: strings.TrimSpace(`
Install short shell aliases for the rig verbs you reach for most:

  rr  → rig run        ri  → rig install
  rup → rig upgrade    rrm → rig uninstall

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
	cmd := &cobra.Command{
		Use:               "install [shell]",
		Short:             "Add the alias block to your shell startup file",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: setupShellCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveSetupShell(args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			snippet := aliasSnippet(shell)
			if printOnly {
				fmt.Fprintln(out, snippet)
				return nil
			}
			rcPath, err := rcFileFor(shell)
			if err != nil {
				return err
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
			fmt.Fprintf(out, "Installed rig aliases (%s) in %s\n", aliasNames(), rcPath)
			fmt.Fprintf(out, "Restart your shell or run: source %s\n", rcPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the snippet instead of writing the rc file")
	return cmd
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

// aliasNames renders the alias names for a one-line summary, e.g. "rr, ri, …".
func aliasNames() string {
	names := make([]string, len(rigAliases))
	for i, a := range rigAliases {
		names[i] = a.name
	}
	return strings.Join(names, ", ")
}

// aliasSnippet renders the marked rc-file block for the shell (no trailing
// newline — installBlock owns the framing). The header uses "#" comments, which
// every supported shell (including PowerShell) understands.
func aliasSnippet(shell string) string {
	begin, end := aliasMarkerBegin(), aliasMarkerEnd()
	lines := []string{
		"# Installed by 'rig alias install' — safe to re-run; replaced in place.",
		"# Short aliases for common rig verbs. Remove with 'rig alias remove'.",
	}
	for _, a := range rigAliases {
		lines = append(lines, aliasLine(shell, a))
	}
	return begin + "\n" + strings.Join(lines, "\n") + "\n" + end
}

// aliasLine renders one alias for the given shell. POSIX shells and fish use
// their alias builtins; PowerShell's Set-Alias can't carry arguments, so the
// alias is a thin function that forwards @args.
func aliasLine(shell string, a rigAlias) string {
	switch shell {
	case "fish":
		return fmt.Sprintf("alias %s '%s %s'", a.name, integrationBase, a.verb)
	case "powershell":
		return fmt.Sprintf("function %s { %s %s @args }", a.name, integrationBase, a.verb)
	default: // zsh, bash
		return fmt.Sprintf("alias %s='%s %s'", a.name, integrationBase, a.verb)
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
