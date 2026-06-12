package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/spf13/cobra"
)

// NewHooksCmd builds the `hooks` command group — install/remove/inspect
// clauderig's Claude Code hooks (SessionStart→pull, Stop→sync) in
// ~/.claude/settings.json. The hooks travel with the synced settings, so a
// restored machine is wired up automatically.
func NewHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install/remove clauderig's Claude Code hooks (SessionStart→pull, Stop→sync)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Add clauderig hooks to ~/.claude/settings.json (idempotent)",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := settingsPath()
				if err != nil {
					return err
				}
				added, err := hooks.Install(path)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if len(added) == 0 {
					fmt.Fprintln(out, OkStyle.Render("✓ already installed"))
					return nil
				}
				fmt.Fprintf(out, "%s installed: %s\n", OkStyle.Render("✓"), strings.Join(added, ", "))
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove clauderig hooks from ~/.claude/settings.json",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := settingsPath()
				if err != nil {
					return err
				}
				removed, err := hooks.Uninstall(path)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed: %s\n", strings.Join(removed, ", "))
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show which events have clauderig hooks",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := settingsPath()
				if err != nil {
					return err
				}
				present, err := hooks.Status(path)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if len(present) == 0 {
					fmt.Fprintln(out, DimStyle.Render("no clauderig hooks installed"))
					return nil
				}
				fmt.Fprintf(out, "installed on: %s\n", strings.Join(present, ", "))
				return nil
			},
		},
	)
	return cmd
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}
