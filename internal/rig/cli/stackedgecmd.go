package cli

import (
	"context"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/spf13/cobra"
)

func newStackEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edge", Short: "Build, publish and check retirement of carried fixes",
		Long: "Build carried fixes under your publishing ids, and check when they can retire.\n\n" +
			"  rig stack edge adopt <member> <pr-url>  bring an upstream PR into a topic\n" +
			"  rig stack edge pack <member>            reconstruct, rename, version, pack and verify\n" +
			"  rig stack edge status [member]          carried fixes, freshness and release evidence\n" +
			"  rig stack edge ci                       build reports or verify and publish packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackEdgePackCmd(), newStackEdgeStatusCmd(), newStackEdgeAdoptCmd(), newStackEdgeCICmd(), newStackEdgeInitCmd())
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
