package cli

import (
	"context"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/spf13/cobra"
)

func newStackEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edge", Short: "Pack carried fixes and check edge retirement",
		Long: "Build carried fixes under your publishing ids, and check when they can retire.\n\n" +
			"  rig stack edge adopt <member> <pr-url>  bring an upstream PR into a topic\n" +
			"  rig stack edge pack <member>            reconstruct, rename, version, pack and verify\n" +
			"  rig stack edge status [member]          carried fixes, freshness and release evidence\n\n" +
			"edge ci and edge init are not built yet.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackEdgePackCmd(), newStackEdgeStatusCmd(), newStackEdgeAdoptCmd())
	return refuseUnknownVerb(cmd)
}

func stackEdgeMemberCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	root, err := stackRoot(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	m, _, err := loadStackManifest(root)
	if err != nil || m == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, name := range m.names() {
		if m.Edge[name] != nil {
			names = append(names, name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
