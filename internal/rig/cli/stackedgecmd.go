package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/spf13/cobra"
)

func newStackEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edge", Short: "Build, publish and check retirement of carried fixes",
		Long: "Build carried fixes under your publishing ids, and check when they can retire.\n\n" +
			"  rig stack edge adopt <member> <pr-url>  bring an upstream PR into a topic\n" +
			"  rig stack edge init [member]            set up publishing; confirm each outward action\n" +
			"  rig stack edge pack <member>            reconstruct, rename, version, pack and verify\n" +
			"  rig stack edge status [member]          carried fixes, freshness and release evidence\n" +
			"  rig stack edge ci                       build reports or verify and publish packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.RunMenu(cmd, cmd.CommandPath(), cmd.Short, stackEdgeMenuEntries())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackEdgePackCmd(), newStackEdgeStatusCmd(), newStackEdgeAdoptCmd(), newStackEdgeCICmd(), newStackEdgeInitCmd())
	return refuseUnknownVerb(cmd)
}

// stackEdgeMenuEntries keeps CI-only flag selections out of the interactive
// menu and offers argument-taking actions through the usual prompting wrappers.
func stackEdgeMenuEntries() []climenu.Entry {
	return []climenu.Entry{
		{Label: "init", Desc: "check and set up edge publishing from the manifest", Cmd: newStackEdgeInitCmd()},
		{Label: "pack", Desc: "pack a member's carried fixes under your ids", Cmd: newStackEdgeMemberMenuCmd(newStackEdgePackCmd())},
		{Label: "status", Desc: "check carried fixes, freshness and retirement", Cmd: newStackEdgeStatusCmd()},
		{Label: "adopt", Desc: "bring an upstream PR into an import-rooted topic", Cmd: newStackEdgeMemberMenuCmd(newStackEdgeAdoptCmd())},
	}
}

// newStackEdgeMemberMenuCmd prompts for the positional inputs that climenu's
// no-argument actions cannot supply, then delegates to the real command.
func newStackEdgeMemberMenuCmd(sub *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use: sub.Name(), Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := stackRoot(cmd.Context())
			if err != nil {
				return err
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("no stack manifest")
			}
			var options []huh.Option[string]
			for _, name := range m.names() {
				if sub.Name() == "pack" && m.Edge[name] == nil {
					continue
				}
				options = append(options, huh.NewOption(name, name))
			}
			if len(options) == 0 {
				return fmt.Errorf("no configured members for edge %s", sub.Name())
			}
			var member, pr string
			fields := []huh.Field{huh.NewSelect[string]().Title("Which member?").Options(options...).Filtering(true).Value(&member)}
			if sub.Name() == "adopt" {
				fields = append(fields, huh.NewInput().Title("Upstream PR URL").Value(&pr))
			}
			if err := stackEdgeMemberPrompt(huh.NewForm(huh.NewGroup(fields...))); err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					return nil
				}
				return err
			}
			args := []string{member}
			if sub.Name() == "adopt" {
				args = append(args, pr)
			}
			sub.SetContext(cmd.Context())
			sub.SetOut(cmd.OutOrStdout())
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, args)
		},
	}
}

var stackEdgeMemberPrompt = func(form *huh.Form) error { return form.Run() }

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
