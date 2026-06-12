package commands

import "github.com/spf13/cobra"

// NewUICmd builds the `ui` command — the hub dashboard (bubbletea): at-a-glance
// remote/local status, per-root state, the device registry, and hotkeys that
// dispatch to the focused TUIs (sync/restore/diff/path-map/config). The real
// bubbletea model lands next; this stub reserves the surface.
func NewUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive dashboard (status, devices, actions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			planned(cmd.OutOrStdout(), "clauderig ui",
				"dashboard   remote reachability · local/behind · last sync · roots · devices",
				"actions     [s] sync  [r] restore  [d] diff  [p] path-map  [c] config",
			)
			return nil
		},
	}
}
