package cli

import (
	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/spf13/cobra"
)

func newStackEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edge", Short: "Pack carried fixes under your publishing ids", Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackEdgePackCmd())
	return refuseUnknownVerb(cmd)
}
