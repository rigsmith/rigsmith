package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	}
	cmd.AddCommand(
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigPathCmd(),
		newConfigEditCmd(),
	)
	return cmd
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

// editFileInEditor opens path in $VISUAL, then $EDITOR, inheriting the terminal.
func editFileInEditor(cmd *cobra.Command, path string) error {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		return fmt.Errorf("no editor set — export $VISUAL or $EDITOR (file: %s)", path)
	}
	fields := strings.Fields(editor)
	ed := exec.CommandContext(cmd.Context(), fields[0], append(fields[1:], path)...)
	ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	return ed.Run()
}
