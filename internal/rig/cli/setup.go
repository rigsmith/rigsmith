package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// `rig setup` installs rig's shell integration: the `rig` wrapper function
// (so `rig cd` can actually change the parent shell's directory — the binary
// prints the target dir to stdout, see cd.go) and cobra's tab-completion
// script, appended idempotently to the shell's rc file between markers so a
// re-run replaces the block in place. The snippet text is adapted from the
// .NET rig's CompletionSetup scripts; the completion engine itself is cobra's
// `rig completion <shell>` (fang registers it) rather than the .NET tool's
// self-contained [suggest] directive.

// setupShells are the shells `rig setup` can write an rc snippet for.
// "pwsh" is accepted as an alias for powershell.
var setupShells = []string{"zsh", "bash", "fish", "powershell"}

// integrationBase is the name cobra bakes into its completion identifiers
// (the function `_rig`, `__start_rig`, `complete -c rig`, …) — always the root
// command's name, independent of which binary name the user invokes it under.
const integrationBase = "rig"

// companionTools are the sibling rig-family binaries whose tab-completion the
// canonical `rig setup` block also loads, so one setup covers the whole family
// (you never run setup once per tool). Each is cobra-based — so `<tool>
// completion <shell>` produces a script — and is sourced only when present on
// PATH, so a machine missing one simply skips it. They need no cd wrapper: none
// has a `cd` subcommand; that's rig's alone.
var companionTools = []string{"shiprig", "changerig", "clauderig"}

// Markers bracketing the managed block in the rc file. They carry the program
// name so a `--dev` block (rig-dev) and the normal block coexist in one rc file
// instead of overwriting each other; a re-run replaces only its own block.
func markerBegin(prog string) string { return "# >>> " + prog + " shell integration >>>" }
func markerEnd(prog string) string   { return "# <<< " + prog + " shell integration <<<" }

// newSetupCmd builds `rig setup [shell] [--print]`.
func newSetupCmd() *cobra.Command {
	var printOnly bool
	var dev bool

	cmd := &cobra.Command{
		Use:   "setup [shell]",
		Short: "Install shell integration (rig cd wrapper + tab completion)",
		Long: strings.TrimSpace(`
Install rig's shell integration into your shell's startup file:

  - the rig() wrapper function, so "rig cd [query]" changes your directory
    (a subprocess can't cd its parent shell; rig prints the dir, the wrapper
    cds to it — everything else passes through to the binary), and
  - tab completion, loaded via cobra's "rig completion <shell>" — for rig and
    the rest of the family (shiprig, changerig, clauderig), so one setup wires
    them all. A companion is loaded only when it's on your PATH.

The shell is taken from the argument, else $SHELL. Supported: zsh, bash, fish,
powershell (alias: pwsh). Startup files: ~/.zshrc, ~/.bashrc,
~/.config/fish/config.fish, and the PowerShell $PROFILE (asked from pwsh, or
the Documents/PowerShell default). The snippet lives between marker comments
and is replaced in place, so re-running is safe.

With --dev the block targets the "rig-dev" launcher instead (its own wrapper,
completion bound to rig-dev, and its own markers) so it coexists with a normal
rig block in the same rc file. Run it as "rig-dev setup zsh --dev".

Use --print to inspect the snippet (or wire it up yourself) without writing.
`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: setupShellCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				return fmt.Errorf("unknown shell %q — supported: %s (rig setup <shell>)",
					shell, strings.Join(setupShells, ", "))
			}

			prog := integrationBase
			if dev {
				prog = integrationBase + "-dev"
			}
			out := cmd.OutOrStdout()
			snippet := setupSnippet(shell, prog)
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

			changed, err := installSnippet(rcPath, snippet, prog)
			if err != nil {
				return fmt.Errorf("couldn't update %s: %w", rcPath, err)
			}
			if !changed {
				fmt.Fprintf(out, "%s shell integration already installed in %s — nothing to do.\n", prog, rcPath)
				return nil
			}
			fmt.Fprintf(out, "Installed %s shell integration (cd wrapper + completion) in %s\n", prog, rcPath)
			fmt.Fprintf(out, "Restart your shell or run: source %s\n", rcPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the snippet instead of writing the rc file")
	cmd.Flags().BoolVar(&dev, "dev", false, "target the rig-dev launcher (own wrapper, completion, and markers)")
	return cmd
}

// setupShellCompletion completes the [shell] arg with the supported shells.
func setupShellCompletion(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return setupShells, cobra.ShellCompDirectiveNoFileComp
}

func isSetupShell(shell string) bool {
	for _, s := range setupShells {
		if s == shell {
			return true
		}
	}
	return false
}

// shellFromEnv guesses the login shell from $SHELL ("" when unset).
func shellFromEnv() string {
	return strings.ToLower(filepath.Base(os.Getenv("SHELL")))
}

// rcFileFor returns the startup file the snippet belongs in. zsh honors
// $ZDOTDIR and fish honors $XDG_CONFIG_HOME, matching where those shells
// actually read their config.
func rcFileFor(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("couldn't resolve a home directory for the rc file")
	}
	switch shell {
	case "zsh":
		if z := os.Getenv("ZDOTDIR"); z != "" {
			return filepath.Join(z, ".zshrc"), nil
		}
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "fish":
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		return filepath.Join(cfg, "fish", "config.fish"), nil
	case "powershell":
		return powershellProfile(home)
	}
	return "", fmt.Errorf("unknown shell %q", shell)
}

// powershellProfile resolves the PowerShell $PROFILE path: an explicit
// $RIG_PWSH_PROFILE wins (also the test seam); otherwise ask pwsh (then
// Windows PowerShell) — $PROFILE is a pwsh variable, not an env var, and
// OneDrive folder redirection means the path can't be derived reliably;
// finally fall back to the conventional Documents location.
func powershellProfile(home string) (string, error) {
	if p := os.Getenv("RIG_PWSH_PROFILE"); p != "" {
		return p, nil
	}
	for _, exe := range []string{"pwsh", "powershell"} {
		out, err := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", "$PROFILE").Output()
		if err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				return p, nil
			}
		}
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
	}
	return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1"), nil
}

// installSnippet writes snippet into the rc file at rcPath: replacing the
// existing marked block for prog when present, appending otherwise. Returns
// false (writing nothing) when the file already carries exactly this snippet.
func installSnippet(rcPath, snippet, prog string) (bool, error) {
	data, err := os.ReadFile(rcPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	updated, changed := spliceSnippet(string(data), snippet, prog)
	if !changed {
		return false, nil
	}
	if dir := filepath.Dir(rcPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}
	return true, os.WriteFile(rcPath, []byte(updated), 0o644)
}

// spliceSnippet returns content with prog's marked block replaced in place, or
// with the snippet appended when no such block exists. Other programs' blocks
// (e.g. a rig block when installing rig-dev) are left untouched. changed is
// false when the existing block already equals snippet (idempotent re-run). Pure.
func spliceSnippet(content, snippet, prog string) (updated string, changed bool) {
	mBegin, mEnd := markerBegin(prog), markerEnd(prog)
	begin := strings.Index(content, mBegin)
	end := strings.Index(content, mEnd)
	if begin >= 0 && end > begin {
		end += len(mEnd)
		if content[begin:end] == snippet {
			return content, false
		}
		return content[:begin] + snippet + content[end:], true
	}
	if strings.TrimSpace(content) == "" {
		return snippet + "\n", true
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + snippet + "\n", true
}

// setupSnippet renders the marked rc-file block for the shell (no trailing
// newline — the writer owns the framing). The wrapper text mirrors the .NET
// rig's CompletionSetup scripts, adapted to cobra completion and to the Go
// binary's `rig cd` contract: the dir on stdout, everything human on stderr.
func setupSnippet(shell, prog string) string {
	begin, end := markerBegin(prog), markerEnd(prog)
	join := func(parts ...string) string { return begin + "\n" + strings.Join(parts, "\n") + "\n" + end }
	switch shell {
	case "fish":
		return join(fishIntegration(prog), companionCompletions(shell, prog))
	case "powershell":
		return join(powershellIntegration(prog), companionCompletions(shell, prog))
	}
	return join(posixHeader(prog), posixCompletion(shell, prog), companionCompletions(shell, prog), posixWrapper(prog))
}

// companionCompletions renders the rc-file lines that load tab-completion for
// each companionTools binary in the given shell, so one `rig setup` wires the
// whole family. Each line is guarded so a tool that isn't installed is skipped
// rather than erroring. It tracks the block it's in: the canonical block binds
// each tool's own name (`clauderig`); a --dev block sources the matching `-dev`
// launcher and rebinds cobra's completer to that name (`clauderig-dev`) — the
// same base-name-vs-invoked-name split posixCompletion does for rig/rig-dev, so
// `clauderig-dev <Tab>` completes against the live build. Returns "" when there
// are no companions. Pure.
func companionCompletions(shell, prog string) string {
	if len(companionTools) == 0 {
		return ""
	}
	dev := prog != integrationBase
	lines := []string{"# Family completions — sibling rigs, each loaded only when it's installed."}
	for _, base := range companionTools {
		cmd := base // the binary we source completion from and bind to
		if dev {
			cmd = base + "-dev"
		}
		switch shell {
		case "fish":
			source := fmt.Sprintf("%s completion fish | source", cmd)
			if dev { // cobra emits `complete -c <base>`; rebind it to the -dev name
				source = fmt.Sprintf("%s completion fish | string replace -a -- '-c %s ' '-c %s ' | source", cmd, base, cmd)
			}
			lines = append(lines, fmt.Sprintf("command -q %s; and %s", cmd, source))
		case "powershell":
			body := "& $__bin completion powershell | Out-String | Invoke-Expression"
			if dev { // re-register cobra's completer (named for the base) under the -dev name
				body += fmt.Sprintf("; Register-ArgumentCompleter -CommandName '%s' -ScriptBlock ${__%sCompleterBlock}", cmd, base)
			}
			lines = append(lines, fmt.Sprintf(
				"$__bin = (Get-Command -Name %s -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source\n"+
					"if ($__bin) { %s }", cmd, body))
		default: // zsh, bash — compinit was already initialized by posixCompletion
			body := fmt.Sprintf(`eval "$(command %s completion %s)"`, cmd, shell)
			if dev {
				switch shell {
				case "zsh":
					body += fmt.Sprintf("; compdef _%s %s", base, cmd)
				case "bash":
					body += fmt.Sprintf("; complete -o default -o nospace -F __start_%s %s 2>/dev/null || complete -o default -F __start_%s %s", base, cmd, base, cmd)
				}
				lines = append(lines, fmt.Sprintf(`command -v %s >/dev/null 2>&1 && { %s; }`, cmd, body))
			} else {
				lines = append(lines, fmt.Sprintf(`command -v %s >/dev/null 2>&1 && %s`, cmd, body))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// posixCompletion sources cobra's completion for zsh/bash. cobra binds the
// completion function to the base name (integrationBase); when prog differs
// (rig-dev), we additionally bind that same function to prog — its runtime
// query uses the typed command, so one function serves both names.
func posixCompletion(shell, prog string) string {
	var b strings.Builder
	if shell == "zsh" {
		// Initialize zsh's completion system if it isn't already (no-op on
		// oh-my-zsh etc.) — cobra's script needs compdef.
		b.WriteString("(( $+functions[compdef] )) || { autoload -Uz compinit && compinit -u 2>/dev/null }\n")
	}
	fmt.Fprintf(&b, `eval "$(command %s completion %s)"`, prog, shell)
	if prog != integrationBase {
		switch shell {
		case "zsh":
			fmt.Fprintf(&b, "\ncompdef _%s %s", integrationBase, prog)
		case "bash":
			fmt.Fprintf(&b, "\ncomplete -o default -o nospace -F __start_%s %s 2>/dev/null \\\n  || complete -o default -F __start_%s %s",
				integrationBase, prog, integrationBase, prog)
		}
	}
	return b.String()
}

func posixHeader(prog string) string {
	return fmt.Sprintf(`# Installed by '%s setup' — safe to re-run; this block is replaced in place.
# Tab completion, plus the '%s cd' wrapper: a subprocess can't change the
# parent shell's directory, so '%s cd [query]' prints the project dir (its
# picker draws on stderr) and the function cds to it. Everything else passes
# straight through to the binary.`, prog, prog, prog)
}

// posixWrapper is the prog() function for zsh and bash (POSIX-compatible). It
// captures stdout for the directory-printing commands — `cd`, `wt cd`, and
// `worktree cd` — and cds the shell there; everything else streams straight
// through.
func posixWrapper(prog string) string {
	return fmt.Sprintf(`%s() {
  if [ "$1" = cd ] || { { [ "$1" = wt ] || [ "$1" = worktree ]; } && [ "$2" = cd ]; }; then
    local __rig_dir
    __rig_dir="$(command %s "$@")" && [ -n "$__rig_dir" ] && builtin cd -- "$__rig_dir"
  else
    command %s "$@"
  fi
}`, prog, prog, prog)
}

// powershellIntegration is the completion + wrapper block for PowerShell
// ($PROFILE). Set-Location is pwsh's cd; the function resolves the real
// binary via Get-Command -CommandType Application so it doesn't recurse
// into itself. When prog differs from the base, the completer (registered by
// cobra for the base name) is also registered for prog.
func powershellIntegration(prog string) string {
	rebind := ""
	if prog != integrationBase {
		rebind = fmt.Sprintf("\n    Register-ArgumentCompleter -CommandName '%s' -ScriptBlock ${__%sCompleterBlock}", prog, integrationBase)
	}
	return fmt.Sprintf(`# Installed by '%s setup' — safe to re-run; this block is replaced in place.
# Tab completion, plus the '%s cd' wrapper: a subprocess can't change the
# parent shell's directory, so '%s cd [query]' prints the project dir (its
# picker draws on stderr) and the function Set-Locations to it. Everything
# else passes straight through to the binary.
$__rigBin = (Get-Command -Name %s -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
if ($__rigBin) {
    & $__rigBin completion powershell | Out-String | Invoke-Expression%s
    function %s {
        $bin = (Get-Command -Name %s -CommandType Application | Select-Object -First 1).Source
        $isCd = ($args.Count -ge 1 -and $args[0] -eq 'cd') -or `+"`"+`
                ($args.Count -ge 2 -and ($args[0] -eq 'wt' -or $args[0] -eq 'worktree') -and $args[1] -eq 'cd')
        if ($isCd) {
            $dir = & $bin @args | Select-Object -Last 1
            if ($LASTEXITCODE -eq 0 -and $dir) { Set-Location -LiteralPath $dir }
        } else {
            & $bin @args
        }
    }
}`, prog, prog, prog, prog, rebind, prog, prog)
}

// fishIntegration is the completion + wrapper block for fish. cobra registers
// its completions for the base name (complete -c rig); when prog differs we
// rewrite that to bind prog instead (the perform-completion helpers it defines
// are shared and query the typed command).
func fishIntegration(prog string) string {
	source := fmt.Sprintf("command %s completion fish | source", prog)
	if prog != integrationBase {
		source = fmt.Sprintf("command %s completion fish | string replace -a -- '-c %s ' '-c %s ' | source",
			prog, integrationBase, prog)
	}
	return fmt.Sprintf(`# Installed by '%s setup' — safe to re-run; this block is replaced in place.
# Tab completion, plus the '%s cd' wrapper: a subprocess can't change the
# parent shell's directory, so '%s cd [query]' prints the project dir (its
# picker draws on stderr) and the function cds to it. Everything else passes
# straight through to the binary.
%s
function %s
    set -l __rig_cd 0
    if test (count $argv) -ge 1; and test "$argv[1]" = cd
        set __rig_cd 1
    else if test (count $argv) -ge 2; and begin; test "$argv[1]" = wt; or test "$argv[1]" = worktree; end; and test "$argv[2]" = cd
        set __rig_cd 1
    end
    if test $__rig_cd -eq 1
        set -l __rig_dir (command %s $argv)
        and test -n "$__rig_dir"
        and builtin cd -- $__rig_dir
    else
        command %s $argv
    end
end`, prog, prog, prog, source, prog, prog, prog)
}
