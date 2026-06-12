package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
var setupShells = []string{"zsh", "bash", "fish"}

// Markers bracketing the managed block in the rc file. A re-run replaces
// everything between them, so upgrades never duplicate the snippet.
const (
	setupBegin = "# >>> rig shell integration >>>"
	setupEnd   = "# <<< rig shell integration <<<"
)

// newSetupCmd builds `rig setup [shell] [--print]`.
func newSetupCmd() *cobra.Command {
	var printOnly bool

	cmd := &cobra.Command{
		Use:   "setup [shell]",
		Short: "Install shell integration (rig cd wrapper + tab completion)",
		Long: strings.TrimSpace(`
Install rig's shell integration into your shell's startup file:

  - the rig() wrapper function, so "rig cd [query]" changes your directory
    (a subprocess can't cd its parent shell; rig prints the dir, the wrapper
    cds to it — everything else passes through to the binary), and
  - tab completion, loaded via cobra's "rig completion <shell>".

The shell is taken from the argument, else $SHELL. Supported: zsh, bash, fish
(rc files ~/.zshrc, ~/.bashrc, ~/.config/fish/config.fish). The snippet lives
between marker comments and is replaced in place, so re-running is safe.

Use --print to inspect the snippet (or wire it up yourself) without writing.
`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: setupShellCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := ""
			if len(args) == 1 {
				shell = strings.ToLower(strings.TrimSpace(args[0]))
			}
			if shell == "" {
				shell = shellFromEnv()
			}
			if !isSetupShell(shell) {
				return fmt.Errorf("unknown shell %q — supported: %s (rig setup <shell>)",
					shell, strings.Join(setupShells, ", "))
			}

			out := cmd.OutOrStdout()
			snippet := setupSnippet(shell)
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

			changed, err := installSnippet(rcPath, snippet)
			if err != nil {
				return fmt.Errorf("couldn't update %s: %w", rcPath, err)
			}
			if !changed {
				fmt.Fprintf(out, "rig shell integration already installed in %s — nothing to do.\n", rcPath)
				return nil
			}
			fmt.Fprintf(out, "Installed rig shell integration (cd wrapper + completion) in %s\n", rcPath)
			fmt.Fprintf(out, "Restart your shell or run: source %s\n", rcPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the snippet instead of writing the rc file")
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
	}
	return "", fmt.Errorf("unknown shell %q", shell)
}

// installSnippet writes snippet into the rc file at rcPath: replacing the
// existing marked block when present, appending otherwise. Returns false
// (writing nothing) when the file already carries exactly this snippet.
func installSnippet(rcPath, snippet string) (bool, error) {
	data, err := os.ReadFile(rcPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	updated, changed := spliceSnippet(string(data), snippet)
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

// spliceSnippet returns content with the marked rig block replaced in place,
// or with the snippet appended when no block exists. changed is false when the
// existing block already equals snippet (idempotent re-run). Pure.
func spliceSnippet(content, snippet string) (updated string, changed bool) {
	begin := strings.Index(content, setupBegin)
	end := strings.Index(content, setupEnd)
	if begin >= 0 && end > begin {
		end += len(setupEnd)
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
func setupSnippet(shell string) string {
	if shell == "fish" {
		return setupBegin + "\n" + fishIntegration + "\n" + setupEnd
	}
	completion := fmt.Sprintf(`eval "$(command rig completion %s)"`, shell)
	if shell == "zsh" {
		// Initialize zsh's completion system if it isn't already (no-op on
		// oh-my-zsh etc.) — cobra's script needs compdef.
		completion = "(( $+functions[compdef] )) || { autoload -Uz compinit && compinit -u 2>/dev/null }\n" + completion
	}
	return setupBegin + "\n" + posixIntegrationHeader + "\n" + completion + "\n" + posixWrapper + "\n" + setupEnd
}

const posixIntegrationHeader = `# Installed by 'rig setup' — safe to re-run; this block is replaced in place.
# Tab completion, plus the 'rig cd' wrapper: a subprocess can't change the
# parent shell's directory, so 'rig cd [query]' prints the project dir (its
# picker draws on stderr) and the function cds to it. Everything else passes
# straight through to the binary.`

// posixWrapper is the rig() function for zsh and bash (POSIX-compatible).
const posixWrapper = `rig() {
  if [ "$1" = cd ]; then
    local __rig_dir
    __rig_dir="$(command rig "$@")" && [ -n "$__rig_dir" ] && builtin cd -- "$__rig_dir"
  else
    command rig "$@"
  fi
}`

// fishIntegration is the completion + wrapper block for fish.
const fishIntegration = `# Installed by 'rig setup' — safe to re-run; this block is replaced in place.
# Tab completion, plus the 'rig cd' wrapper: a subprocess can't change the
# parent shell's directory, so 'rig cd [query]' prints the project dir (its
# picker draws on stderr) and the function cds to it. Everything else passes
# straight through to the binary.
command rig completion fish | source
function rig
    if test (count $argv) -gt 0; and test "$argv[1]" = cd
        set -l __rig_dir (command rig $argv)
        and test -n "$__rig_dir"
        and builtin cd -- $__rig_dir
    else
        command rig $argv
    end
end`
