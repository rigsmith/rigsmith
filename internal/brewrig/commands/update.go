package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// NewUpdateCmd upgrades this machine, then republishes so the other machine can
// see the new versions.
func NewUpdateCmd() *cobra.Command {
	var greedy bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh Homebrew and upgrade everything outdated here, then publish",
		Long: "Runs `brew update` then `brew upgrade`, and publishes the resulting versions.\n\n" +
			"This is the fix for version skew: run it on whichever machine `brewrig status`\n" +
			"reports as behind, and the two converge on current versions. brewrig does not\n" +
			"pin or downgrade — it moves machines forward, never back.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), cmd.OutOrStdout(), greedy)
		},
	}
	cmd.Flags().BoolVar(&greedy, "greedy", false, "also upgrade casks that auto-update themselves (may restart apps)")
	return cmd
}

func runUpdate(ctx context.Context, out io.Writer, greedy bool) error {
	s, err := open(ctx, true)
	if err != nil {
		return err
	}
	greedy = greedy || s.cfg.GreedyCasks

	fmt.Fprintf(out, "%s brew update\n", AccentStyle.Render("→"))
	if err := s.mut.Update(ctx); err != nil {
		return err
	}

	outdated, err := s.brew.Outdated(ctx)
	if err != nil {
		return err
	}
	if len(outdated) == 0 && !greedy {
		fmt.Fprintf(out, "%s everything is current\n", OkStyle.Render("✓"))
		// Still republish: `brew update` can change nothing locally while the
		// last published snapshot is stale for other reasons, and a no-op
		// publish costs nothing because an unchanged file makes no commit.
		return republish(ctx, s, out)
	}
	for _, r := range outdated {
		fmt.Fprintf(out, "  %s %s\n", WarnStyle.Render("↑"), r.Label())
	}

	fmt.Fprintf(out, "%s brew upgrade\n", AccentStyle.Render("→"))
	if err := s.mut.Upgrade(ctx, greedy); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s upgraded\n", OkStyle.Render("✓"))
	return republish(ctx, s, out)
}
