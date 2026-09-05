package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
	"github.com/rigsmith/rigsmith/internal/clauderig/tui"
	"github.com/spf13/cobra"
)

// NewUICmd builds the `ui` command — the hub dashboard. It shows the gathered
// status and, on a hotkey, dispatches to the matching command (sync/restore/
// status) after the program exits, so heavy work never runs in the event loop.
func NewUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive dashboard (status, devices, actions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, _ := config.StagingDir()
			settings, _ := settingsPath()
			info := status.Gather(ctx, cfg, me, staging, settings)

			// Gathered here rather than inside status.Gather: it walks the
			// transcript tree, and Gather is on the tray's five-second poll.
			home, _ := cfg.RootLocation("cli", me)
			filing := sessions.CheckHealth(
				[]search.Target{{Label: sessions.CLISource, Dir: home}},
				sessions.Roots(cfg, me, false, false),
			)

			res, err := tea.NewProgram(tui.New(info).WithFiling(filing)).Run()
			if err != nil {
				return err
			}
			final, ok := res.(tui.Model)
			if !ok {
				return nil
			}
			switch final.Chosen {
			case "init":
				return NewInitCmd().RunE(cmd, nil)
			case "sync":
				return NewSyncCmd().RunE(cmd, nil)
			case "restore":
				return NewRestoreCmd().RunE(cmd, nil)
			case "status":
				return NewStatusCmd().RunE(cmd, nil)
			case "mcp":
				return runMCPUI(cmd)
			case "account":
				return runAccountUI(cmd)
			case "desktop":
				return runDesktopUI(cmd)
			}
			return nil
		},
	}
}
