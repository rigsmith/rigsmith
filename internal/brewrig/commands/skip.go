package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
	"github.com/spf13/cobra"
)

// NewSkipCmd records deliberate divergence: a package this machine does not
// want even though another machine has it.
func NewSkipCmd() *cobra.Command {
	var cask bool
	cmd := &cobra.Command{
		Use:   "skip <package>",
		Short: "Stop offering a package on this machine",
		Long: "Records that this machine deliberately doesn't want a package the others have,\n" +
			"so `apply` stops proposing it. The decision is published, so the other machine\n" +
			"can see it rather than re-proposing it forever.\n\n" +
			"Undo it by installing the package normally — an install always beats a skip.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := inventory.Formula
			if cask {
				kind = inventory.Cask
			}
			return runSkip(cmd.Context(), cmd.OutOrStdout(), inventory.Ref{Kind: kind, Name: args[0]})
		},
	}
	cmd.Flags().BoolVar(&cask, "cask", false, "the package is a cask, not a formula")
	return cmd
}

func runSkip(ctx context.Context, out io.Writer, r inventory.Ref) error {
	s, err := open(ctx, true)
	if err != nil {
		return err
	}
	self, _, err := s.snapshot(ctx, nowUTC())
	if err != nil {
		return err
	}
	if self.Has(r) {
		return fmt.Errorf("%s is installed here — `brew uninstall %s` first, "+
			"which records it as a deliberate removal", r.Label(), r.Name)
	}
	if err := s.optOut(ctx, r); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s will not be offered on %s\n", OkStyle.Render("✓"), r.Label(), AccentStyle.Render(s.cfg.Machine))
	return nil
}
