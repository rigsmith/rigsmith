package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ghrepo"
	"github.com/spf13/cobra"
)

// saveConfig writes cfg to the config dir, creating the dir if needed.
func saveConfig(cfg *config.Config) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return config.Save(cfg, dir)
}

// configKeys documents the settable keys for `config set <key> <value>`, in the
// order shown by an unknown-key error.
var configKeys = []string{"remote", "alwaysPrune", "autoRestore"}

// NewConfigCmd builds the uniform `config` command group: get / set / path /
// edit (and the legacy-friendly `show`). `set remote` enforces the same
// private-repo gate as init.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change claudeRig configuration",
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

// configPath returns ~/.clauderig/config.json.
func configPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the whole configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("no config yet — run `clauderig init`"))
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print one setting (or all known settings when no key is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				for _, k := range configKeys {
					v, _ := configValue(cfg, k)
					fmt.Fprintf(out, "%s = %s\n", k, v)
				}
				return nil
			}
			v, ok := configValue(cfg, args[0])
			if !ok {
				return unknownKeyErr(args[0])
			}
			fmt.Fprintln(out, v)
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one setting: " + strings.Join(configKeys, ", "),
		Long: "Set a claudeRig setting. Known keys:\n" +
			"  remote             sync remote URL (verified private via gh/glab)\n" +
			"  alwaysPrune        prune stale config on `restore` by default (bool)\n" +
			"  autoRestore        auto-restore on a fresh machine via SessionStart (bool)",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			msg, err := applyConfigSet(cmd, cfg, key, value)
			if err != nil {
				return err
			}
			if err := saveConfig(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("✓"), msg)
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $VISUAL/$EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			return openInEditor(cmd, path)
		},
	}
}

// applyConfigSet validates and applies key=value onto cfg, returning a confirmation
// message. The `remote` key runs the private-repo gate; bools accept 1/0/true/
// false/yes/no/on/off.
func applyConfigSet(cmd *cobra.Command, cfg *config.Config, key, value string) (string, error) {
	switch key {
	case "remote":
		if err := ghrepo.EnsurePrivate(cmd.Context(), value); err != nil {
			return "", err
		}
		cfg.Remote = value
		return "remote set to " + value, nil
	case "alwaysPrune":
		on, err := parseBoolArg(value)
		if err != nil {
			return "", err
		}
		cfg.AlwaysPrune = on
		return fmt.Sprintf("alwaysPrune = %v", on), nil
	case "autoRestore":
		on, err := parseBoolArg(value)
		if err != nil {
			return "", err
		}
		cfg.AutoRestore = on
		return fmt.Sprintf("autoRestore = %v", on), nil
	default:
		return "", unknownKeyErr(key)
	}
}

// configValue renders the current value of a known key for `config get`.
func configValue(cfg *config.Config, key string) (string, bool) {
	switch key {
	case "remote":
		return cfg.Remote, true
	case "alwaysPrune":
		return fmt.Sprintf("%v", cfg.AlwaysPrune), true
	case "autoRestore":
		return fmt.Sprintf("%v", cfg.AutoRestore), true
	default:
		return "", false
	}
}

func unknownKeyErr(key string) error {
	keys := append([]string(nil), configKeys...)
	sort.Strings(keys)
	return fmt.Errorf("unknown config key %q (known: %s)", key, strings.Join(keys, ", "))
}

// parseBoolArg accepts the usual on/off spellings for a bool config value.
func parseBoolArg(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("expected a boolean (true/false), got %q", s)
}

// openInEditor opens path in $VISUAL, then $EDITOR, inheriting the terminal.
func openInEditor(cmd *cobra.Command, path string) error {
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
