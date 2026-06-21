package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// configScalarKeys are the scalar .rig.json knobs `rig config set` understands.
// Richer fields (env, commands, aliases, tools, coverage, …) stay in
// `rig config edit` — they don't reduce to a single get/set value.
var configScalarKeys = []string{"solution", "defaultProject", "ecosystem", "quiet", "worktree.autoOpen", "worktree.openCmd"}

// newConfigCmd builds the uniform `config` group: get / set / path / edit over
// the repo's .rig.json (with the user-wide ~/.rig.json layered under `get`).
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change .rig.json",
		// Bare `config` on a TTY opens the subcommand menu; with a verb or off a
		// TTY the subcommands stand (and `config -h` still prints help).
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigPathCmd(),
		newConfigEditCmd(),
	)
	return cmd
}

// newConfigShowCmd prints the repo's whole .rig.json, matching changerig/
// clauderig `config show`.
func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the whole .rig.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(repoConfigPath())
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintln(cmd.OutOrStdout(), "no .rig.json yet — run `rig init`")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(data), "\n"))
			return nil
		},
	}
}

// repoConfigPath returns the repo's .rig.json path for the current root.
func repoConfigPath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(resolveRoot(cwd), config.FileName)
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print one scalar setting, or all of them (repo merged over global)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			cfg, err := config.LoadMerged(resolveRoot(cwd))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				for _, k := range configScalarKeys {
					v, _ := configScalarValue(cfg, k)
					fmt.Fprintf(out, "%s = %s\n", k, v)
				}
				return nil
			}
			v, ok := configScalarValue(cfg, args[0])
			if !ok {
				return unknownConfigKeyErr(args[0])
			}
			fmt.Fprintln(out, v)
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one scalar setting in the repo's .rig.json (comments preserved)",
		Long: "Set a scalar key in the repo's .rig.json. Keys:\n" +
			"  solution        the .sln/.slnx the .NET verbs operate on\n" +
			"  defaultProject  project `rig run` targets when none is named\n" +
			"  ecosystem         pin the primary ecosystem (dotnet|node|go|cargo)\n" +
			"  quiet             suppress the `→ command` echo (bool)\n" +
			"  worktree.autoOpen `rig worktree new` opens a review window (bool, default false)\n" +
			"  worktree.openCmd  command to open a worktree (path appended), e.g. \"code -n\"",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			path := filepath.Join(root, config.FileName)

			var ok bool
			switch key {
			case "solution", "defaultProject", "ecosystem":
				ok = config.SetString(path, []string{key}, value)
			case "quiet":
				on, err := parseConfigBool(value)
				if err != nil {
					return err
				}
				ok = config.SetBool(path, []string{key}, on)
			case "worktree.autoOpen":
				on, err := parseConfigBool(value)
				if err != nil {
					return err
				}
				ok = config.SetBool(path, []string{"worktree", "autoOpen"}, on)
			case "worktree.openCmd":
				ok = config.SetString(path, []string{"worktree", "openCmd"}, value)
			default:
				return unknownConfigKeyErr(key)
			}
			if !ok {
				return fmt.Errorf("could not write %s (refusing to clobber a file that can't be edited in place)", path)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the repo and user-wide config paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, repoConfigPath())
			if gp := config.GlobalPath(); gp != "" {
				fmt.Fprintln(out, gp+"  (global)")
			}
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the repo's .rig.json in $VISUAL/$EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return editFileInEditor(cmd, repoConfigPath())
		},
	}
}

// configScalarValue renders the effective value of a scalar key from a (merged)
// config, with the same names accepted by `config set`.
func configScalarValue(cfg config.Config, key string) (string, bool) {
	switch key {
	case "solution":
		return cfg.Solution, true
	case "defaultProject":
		return cfg.DefaultProject, true
	case "ecosystem":
		return cfg.Ecosystem, true
	case "quiet":
		return fmt.Sprintf("%v", cfg.IsQuiet()), true
	case "worktree.autoOpen":
		return fmt.Sprintf("%v", cfg.WorktreeAutoOpen()), true
	case "worktree.openCmd":
		return strings.Join(cfg.WorktreeOpenCmd(), " "), true
	default:
		return "", false
	}
}

func unknownConfigKeyErr(key string) error {
	keys := append([]string(nil), configScalarKeys...)
	sort.Strings(keys)
	return fmt.Errorf("unknown config key %q (settable: %s) — use `rig config edit` for other fields", key, strings.Join(keys, ", "))
}

func parseConfigBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("expected a boolean (true/false), got %q", s)
}

// editFileInEditor opens path in the resolved editor, inheriting the terminal so
// the edit is interactive (or, for a GUI editor, so we block until it closes).
func editFileInEditor(cmd *cobra.Command, path string) error {
	argv := resolveEditorArgv(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS, exec.LookPath, bundleExists, path)
	ed := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	return ed.Run()
}

// guiEditors are GUI editors auto-launched when neither $VISUAL nor $EDITOR is
// set, in preference order. Each is launched blocking (--wait) so the edit
// completes before we re-read the file.
var guiEditors = []struct {
	cmd  string
	args []string
}{
	{"code", []string{"--wait"}},          // VS Code
	{"code-insiders", []string{"--wait"}}, // VS Code Insiders
	{"cursor", []string{"--wait"}},        // Cursor
}

// macAppBundles are the macOS .app fallbacks probed when a GUI editor's CLI
// isn't on PATH. They're opened via `open -W -a <app>`, which blocks until the
// app's window for the file is closed.
var macAppBundles = []struct {
	appName    string
	bundlePath string
}{
	{"Visual Studio Code", "/Applications/Visual Studio Code.app"},
	{"Cursor", "/Applications/Cursor.app"},
}

// resolveEditorArgv decides how to open path. Precedence: $VISUAL, then $EDITOR
// (splitting on spaces honors forms like "code --wait"); else the first detected
// GUI editor — by PATH command, then macOS .app bundle — launched blocking; else
// a per-OS terminal default (notepad on Windows, vi elsewhere). Always returns a
// runnable argv. Pure given lookPath and bundleExists.
func resolveEditorArgv(visual, editorEnv, goos string, lookPath func(string) (string, error), bundleExists func(string) bool, path string) []string {
	if editor := firstNonEmpty(visual, editorEnv); editor != "" {
		return append(strings.Fields(editor), path)
	}
	for _, e := range guiEditors {
		if _, err := lookPath(e.cmd); err == nil {
			return append(append([]string{e.cmd}, e.args...), path)
		}
	}
	if goos == "darwin" {
		for _, b := range macAppBundles {
			if bundleExists(b.bundlePath) {
				return []string{"open", "-W", "-a", b.appName, path}
			}
		}
	}
	if goos == "windows" {
		return []string{"notepad", path}
	}
	return []string{"vi", path}
}

// firstNonEmpty returns the first argument that isn't blank (after trimming),
// else "".
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if s := strings.TrimSpace(x); s != "" {
			return s
		}
	}
	return ""
}

// bundleExists reports whether p is an existing directory (a macOS .app bundle).
func bundleExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
