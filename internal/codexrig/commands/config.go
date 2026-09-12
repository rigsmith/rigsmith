package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/editor"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/spf13/cobra"
)

// configKeys are the settings `config get`/`set` expose, in the order a person
// would meet them.
var configKeys = []string{"remote", "syncSessions", "redactTranscripts", "chunkRollouts", "autoRestore", "alwaysPrune", "hookIntervalMinutes"}

// NewConfigCmd builds `codexrig config`.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change codexrig's own settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newConfigShowCmd(), newConfigGetCmd(), newConfigSetCmd(),
		newConfigPathCmd(), newConfigEditCmd(),
	)
	return cmd
}

func configPath() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the config file as it is on disk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := configPath()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("no config yet — run `codexrig init`"))
				return nil
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Read one setting, or all of them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				for _, k := range configKeys {
					fmt.Fprintf(out, "%s = %s\n", k, configValue(cfg, k))
				}
				return nil
			}
			if !known(args[0]) {
				return unknownKey(args[0])
			}
			fmt.Fprintln(out, configValue(cfg, args[0]))
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one setting",
		Long: "remote               the private git repo this machine syncs to\n" +
			"syncSessions         carry session rollouts as well as configuration\n" +
			"chunkRollouts        store a large rollout as parts, so an append costs a chunk not a copy\n" +
			"redactTranscripts    scrub credential-shaped tokens out of staged rollouts\n" +
			"autoRestore          restore automatically on a machine with no Codex setup\n" +
			"alwaysPrune          make `restore` prune by default\n" +
			"hookIntervalMinutes  how long a hook-driven sync waits before working again (0 = never wait)",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if !known(key) {
				return unknownKey(key)
			}
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			msg, err := applyConfigSet(cmd, cfg, key, value)
			if err != nil {
				return err
			}
			dir, err := config.Dir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			if err := config.Save(cfg, dir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("✓"), msg)
			return nil
		},
	}
}

func applyConfigSet(cmd *cobra.Command, cfg *config.Config, key, value string) (string, error) {
	switch key {
	case "remote":
		// The same gate `init` uses. A remote set by hand must clear it too, or
		// the one path that skips the check is the one people take.
		if err := ghrepo.EnsurePrivate(cmd.Context(), value); err != nil {
			return "", err
		}
		cfg.Remote = value
		return "remote set to " + value, nil
	case "syncSessions":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		cfg.SyncSessions = on
		if on {
			return "syncSessions = true (rollouts will be carried from the next sync)", nil
		}
		return "syncSessions = false (rollouts already in the repo are retired on the next sync)", nil
	case "redactTranscripts":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		cfg.RedactTranscripts = on
		return fmt.Sprintf("redactTranscripts = %v (applies on the next sync)", on), nil
	case "chunkRollouts":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		cfg.ChunkRollouts = &on
		if on {
			return "chunkRollouts = true (large rollouts become content-addressed parts on the next sync)", nil
		}
		return "chunkRollouts = false (they become single files again on the next sync)", nil
	case "autoRestore":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		cfg.AutoRestore = on
		return fmt.Sprintf("autoRestore = %v", on), nil
	case "alwaysPrune":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		cfg.AlwaysPrune = on
		return fmt.Sprintf("alwaysPrune = %v", on), nil
	case "hookIntervalMinutes":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return "", fmt.Errorf("expected a whole number of minutes, got %q", value)
		}
		cfg.HookIntervalMinutes = &n
		if n <= 0 {
			return "hookIntervalMinutes = 0 (every hook run syncs)", nil
		}
		return fmt.Sprintf("hookIntervalMinutes = %d", n), nil
	}
	return "", unknownKey(key)
}

func configValue(cfg *config.Config, key string) string {
	switch key {
	case "remote":
		if cfg.Remote == "" {
			return "(none)"
		}
		return cfg.Remote
	case "syncSessions":
		return strconv.FormatBool(cfg.SyncSessions)
	case "redactTranscripts":
		return strconv.FormatBool(cfg.RedactTranscripts)
	case "chunkRollouts":
		if cfg.ChunkRollouts == nil {
			return "true (default)"
		}
		return strconv.FormatBool(*cfg.ChunkRollouts)
	case "autoRestore":
		return strconv.FormatBool(cfg.AutoRestore)
	case "alwaysPrune":
		return strconv.FormatBool(cfg.AlwaysPrune)
	case "hookIntervalMinutes":
		if cfg.HookIntervalMinutes == nil {
			return fmt.Sprintf("%d (default)", config.DefaultHookIntervalMinutes)
		}
		return strconv.Itoa(*cfg.HookIntervalMinutes)
	}
	return ""
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print where the config file lives",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := configPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in your editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := configPath()
			if err != nil {
				return err
			}
			argv := editor.Argv(p)
			if len(argv) == 0 {
				return fmt.Errorf("no editor found — set $EDITOR")
			}
			return runInteractive(argv)
		},
	}
}

func known(key string) bool {
	for _, k := range configKeys {
		if k == key {
			return true
		}
	}
	return false
}

func unknownKey(key string) error {
	keys := append([]string(nil), configKeys...)
	sort.Strings(keys)
	return fmt.Errorf("unknown config key %q (known: %s)", key, strings.Join(keys, ", "))
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("expected a boolean (true/false), got %q", v)
}
