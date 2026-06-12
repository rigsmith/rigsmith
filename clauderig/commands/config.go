package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/ghrepo"
	"github.com/spf13/cobra"
)

// NewConfigCmd builds the `config` command group — view the config and change the
// sync remote. set-remote enforces the same private-repo gate as init.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change clauderig configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Print the current configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				dir, err := config.Dir()
				if err != nil {
					return err
				}
				b, err := os.ReadFile(filepath.Join(dir, "config.json"))
				if err != nil {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("no config yet — run `clauderig init`"))
					return nil
				}
				fmt.Fprint(cmd.OutOrStdout(), string(b))
				return nil
			},
		},
		&cobra.Command{
			Use:   "set-prune <true|false>",
			Short: "Set whether `restore` prunes stale config files by default",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				on := args[0] == "true" || args[0] == "1" || args[0] == "yes"
				cfg, err := config.LoadOrDefault()
				if err != nil {
					return err
				}
				cfg.AlwaysPrune = on
				dir, err := config.Dir()
				if err != nil {
					return err
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				if err := config.Save(cfg, dir); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s alwaysPrune = %v\n", OkStyle.Render("✓"), on)
				return nil
			},
		},
		&cobra.Command{
			Use:   "set-remote <url>",
			Short: "Set the sync remote (verified private via gh)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				url := args[0]
				if err := ghrepo.EnsurePrivate(cmd.Context(), url); err != nil {
					return err
				}
				cfg, err := config.LoadOrDefault()
				if err != nil {
					return err
				}
				cfg.Remote = url
				dir, err := config.Dir()
				if err != nil {
					return err
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				if err := config.Save(cfg, dir); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s remote set to %s\n", OkStyle.Render("✓"), url)
				return nil
			},
		},
	)
	return cmd
}
