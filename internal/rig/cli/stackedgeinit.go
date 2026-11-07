package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// These use rig's terminal gate and confirm widget. Tests can supply input to
// the same widget without borrowing the developer's terminal.
var stackEdgeSetupTTY = stdinStdoutTTY
var stackEdgeSetupPrompt = runHuhConfirm
var stackEdgeSetupPack = stackEdgeSetupVerifyPack

func newStackEdgeInitCmd() *cobra.Command {
	var preview bool
	var authRef string
	cmd := &cobra.Command{
		Use: "init [member]", Hidden: true,
		Short: "Check and set up edge publishing from the manifest",
		Long: "Checks the shared edge setup facts, repairs missing facts, then checks them again.\n" +
			"Manifest edits and consumer NuGet configuration are printed only. Outward actions\n" +
			"each require interactive confirmation on a terminal. No confirmation bypass exists.",
		Args: cobra.MaximumNArgs(1), ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stackEdgeSetupCommand(cmd, args, true, preview || dryRun, authRef)
		},
	}
	cmd.Flags().BoolVarP(&preview, "dry-run", "n", false, "print facts and planned repairs without making changes or building")
	cmd.Flags().StringVar(&authRef, "push-auth", "", "push credential reference (op://…, env:NAME or cmd:…); otherwise read from stdin")
	_ = cmd.RegisterFlagCompletionFunc("push-auth", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func stackEdgeSetupCommand(cmd *cobra.Command, args []string, repair, preview bool, authRef string) error {
	root, err := stackRoot(cmd.Context())
	if err != nil {
		return err
	}
	m, _, err := loadStackManifestForEdgeSetup(root, true)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no stack manifest")
	}
	var names []string
	if len(args) != 0 {
		if m.Repos[args[0]] == nil {
			return fmt.Errorf("unknown stack member %q", args[0])
		}
		names = args
	} else {
		for _, name := range m.names() {
			if m.Edge[name] != nil {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("no edge members configured; add an edge block for a member in the manifest")
	}
	s := &stackEdgeSetup{ctx: cmd.Context(), root: root, m: m, out: cmd.OutOrStdout(), in: cmd.InOrStdin(), api: stackDepsClient(), authRef: authRef,
		tty: stackEdgeSetupTTY, prompt: stackEdgeSetupPrompt, pack: stackEdgeSetupPack, run: stackEdgeSetupExec, now: stackEdgeSetupNow, sleep: stackEdgeSetupSleep}
	return s.checkFacts(names, repair, preview)
}

func (s *stackEdgeSetup) confirm(action string) error {
	if !s.tty() {
		return fmt.Errorf("would %s; requires interactive confirmation with stdin and stdout on a terminal", action)
	}
	yes := false
	if err := s.prompt(huh.NewConfirm().Title(action + "?").Value(&yes)); err != nil || !yes {
		return fmt.Errorf("cancelled: would %s", action)
	}
	return nil
}

func stackEdgeSetupVerifyPack(ctx context.Context, out io.Writer, root, name string) error {
	return stackEdgeSetupVerifyPackWith(ctx, out, root, name, stackEdgeReconstructOptions{
		Checkout: root, Members: []string{name}, AllowedHosts: "github.com",
	}, stackEdgePackOptions{})
}

// Supplied reconstruction/feed options let the gated pack test use bare local
// remotes and a fake public feed. Output and run number are always owned here.
func stackEdgeSetupVerifyPackWith(ctx context.Context, out io.Writer, root, name string, reconstruct stackEdgeReconstructOptions, opts stackEdgePackOptions) error {
	fmt.Fprintf(out, "%s: reconstructing and building the member runs project code and can take a while (temporary, unpublishable pack; number 0)\n", name)
	dir, err := os.MkdirTemp("", "rig-edge-setup-pack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	rebuilt, cleanup, err := stackEdgeReconstruct(ctx, reconstruct)
	if err != nil {
		return err
	}
	defer cleanup()
	m, _, err := loadStackManifest(rebuilt.Root)
	if err != nil {
		return err
	}
	opts.Checkout, opts.OutputDir, opts.Number = root, filepath.Join(dir, "packages"), 0
	opts.Commit, opts.Provenance = rebuilt.Provenance[name].TrackCommit, rebuilt.Provenance
	opts.Here, opts.DryRun = false, false
	result, err := stackEdgePackTree(ctx, out, m, rebuilt.Root, name, opts)
	if err != nil {
		return err
	}
	if result.Publishable {
		return fmt.Errorf("setup pack must be unpublishable")
	}
	return nil
}
