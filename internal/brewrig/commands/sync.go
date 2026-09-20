package commands

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/engine"
	"github.com/spf13/cobra"
)

type syncOpts struct {
	apply bool
}

// NewSyncCmd publishes this machine's inventory.
func NewSyncCmd() *cobra.Command {
	var o syncOpts
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Publish this machine's Homebrew inventory and show how it differs",
		Long: "Reads what this machine has deliberately installed, publishes it to the shared\n" +
			"private repo, and reports what the machines now differ on. Publishing only —\n" +
			"nothing is installed or removed unless you pass --apply (which still never\n" +
			"removes without asking).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd.Context(), cmd.OutOrStdout(), o)
		},
	}
	cmd.Flags().BoolVar(&o.apply, "apply", false, "also install what this machine is missing")
	return cmd
}

func runSync(ctx context.Context, out io.Writer, o syncOpts) error {
	s, err := open(ctx, true)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	self, retired, err := s.snapshot(ctx, now)
	if err != nil {
		return err
	}
	if err := s.store.EnsureReadme(); err != nil {
		return err
	}
	if err := s.store.Write(self); err != nil {
		return err
	}

	// A sync that changed nothing must not commit: two machines committing
	// reordered-but-identical files at each other is pure noise, and it is why
	// the inventory is normalized before it is written.
	pushed, err := s.store.Publish(ctx, fmt.Sprintf("%s: %d formulae, %d casks", self.Name, len(self.Formulae), len(self.Casks)))
	if err != nil {
		return err
	}
	if pushed {
		fmt.Fprintf(out, "%s published %s (%d formulae, %d casks)\n",
			OkStyle.Render("✓"), AccentStyle.Render(self.Name), len(self.Formulae), len(self.Casks))
	} else {
		fmt.Fprintf(out, "%s %s unchanged (%d formulae, %d casks)\n",
			DimStyle.Render("·"), AccentStyle.Render(self.Name), len(self.Formulae), len(self.Casks))
	}
	for _, r := range retired {
		fmt.Fprintf(out, "  %s recorded that you removed %s here\n", WarnStyle.Render("−"), r.Label())
	}

	_, p, err := s.planNow(ctx, now)
	if err != nil {
		return err
	}
	printPlan(out, p)

	if !o.apply && !s.cfg.AutoApply {
		if len(p.Install) > 0 {
			fmt.Fprintf(out, "\n%s\n", DimStyle.Render("run `brewrig apply` to install them"))
		}
		return nil
	}
	if len(p.Install) == 0 && len(p.Taps) == 0 {
		return nil
	}
	fmt.Fprintln(out)
	// Removals are deliberately not part of --apply: they need a confirmation
	// per package, which `apply` handles on a terminal.
	res := engine.Apply(ctx, s.mut, p, stepPrinter())
	err = reportResult(res)
	// Homebrew changed, so the inventory just published is already stale.
	// Republish before returning, including when some actions failed —
	// whatever DID install is real, and leaving the shared copy behind makes
	// the other machine re-propose it.
	return finishMutation(res, err, func() error { return republish(ctx, s, out) })
}
