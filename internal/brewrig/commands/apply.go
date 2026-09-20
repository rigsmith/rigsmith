package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/brewrig/engine"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
	"github.com/rigsmith/rigsmith/internal/brewrig/plan"
	"github.com/spf13/cobra"
)

// NewApplyCmd brings this machine up to the union.
func NewApplyCmd() *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Install what this machine is missing (and ask about anything removed elsewhere)",
		Long: "Installs everything your other machines have that this one doesn't.\n\n" +
			"It will not uninstall anything to make the machines match: a package only this\n" +
			"Mac has is something the others haven't caught up on. The single exception is a\n" +
			"package you deliberately uninstalled on another machine — that is offered here\n" +
			"one at a time, and only on a terminal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd.Context(), cmd.OutOrStdout(), dryRun, yes)
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print what would change and stop")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation before installing (removals still ask)")
	return cmd
}

func runApply(ctx context.Context, out io.Writer, dryRun, yes bool) error {
	s, err := open(ctx, true)
	if err != nil {
		return err
	}
	self, p, err := s.planNow(ctx, time.Now().UTC())
	if err != nil {
		return err
	}

	printPlan(out, p)
	if p.Empty() {
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "\n%s\n", DimStyle.Render("dry run — nothing changed"))
		return nil
	}

	var res *engine.Result
	if len(p.Install) > 0 || len(p.Taps) > 0 {
		ok := yes || !interactive()
		if !ok {
			if err := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Install %d package(s) on %s?", len(p.Install), self.Name)).
					Value(&ok),
			)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
				return err
			}
		}
		if ok {
			fmt.Fprintln(out)
			res = engine.Apply(ctx, s.mut, p, stepPrinter())
		}
	}

	removed, err := confirmRemovals(ctx, out, s, p)
	if err != nil {
		return err
	}

	if res == nil {
		res = &engine.Result{}
	}
	res.Removed = append(res.Removed, removed.Removed...)
	res.Failed = append(res.Failed, removed.Failed...)
	if !res.Any() && len(res.Failed) == 0 {
		fmt.Fprintf(out, "\n%s\n", DimStyle.Render("nothing applied"))
		return nil
	}
	fmt.Fprintln(out)
	err = reportResult(res)

	// Republish whenever anything actually changed, including a partial run.
	// Returning on the first failure would leave the shared copy stale while
	// Homebrew here had already moved, so the other machine would re-propose
	// packages this one now has.
	if res.Any() {
		// Join rather than prefer one: an action failure and a republish
		// failure are different problems, and dropping the republish error
		// hides that Homebrew moved while the shared inventory did not.
		if rerr := republish(ctx, s, out); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

// confirmRemovals asks about each proposed uninstall separately.
//
// One prompt per package, never a batch: a single "remove 6 packages?" hides
// the one in the list you would have said no to, and this is the only operation
// in brewrig that destroys something.
func confirmRemovals(ctx context.Context, out io.Writer, s *session, p *plan.Plan) (*engine.Result, error) {
	if len(p.Remove) == 0 {
		return &engine.Result{}, nil
	}
	if !interactive() {
		fmt.Fprintf(out, "\n%s %d package(s) were removed on another machine; run `brewrig apply` on a terminal to decide\n",
			WarnStyle.Render("!"), len(p.Remove))
		return &engine.Result{}, nil
	}

	var confirmed []inventory.Ref
	for _, rm := range p.Remove {
		yes := false
		title := fmt.Sprintf("Uninstall %s here too?", rm.Ref.Label())
		desc := fmt.Sprintf("%s removed it on %s.", rm.By, rm.At.Local().Format("2 Jan 2006"))
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title(title).Description(desc).
				Affirmative("Uninstall").Negative("Keep").Value(&yes),
		)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
			// Escaping out of the prompts means "stop asking", not "yes to
			// the rest".
			break
		}
		if yes {
			confirmed = append(confirmed, rm.Ref)
		} else {
			// Keeping it is a decision, not a deferral. Recording it stops the
			// same prompt appearing on every future run — which is what would
			// otherwise train you to dismiss it without reading. A later,
			// separate retirement of the same package is still offered.
			if err := s.acknowledge(ctx, rm.Ref, rm.At); err != nil {
				return nil, err
			}
			fmt.Fprintf(out, "  %s keeping %s — noted, it won't be offered again\n",
				DimStyle.Render("·"), rm.Ref.Label())
		}
	}
	if len(confirmed) == 0 {
		return &engine.Result{}, nil
	}
	fmt.Fprintln(out)
	return engine.ApplyRemovals(ctx, s.mut, confirmed, stepPrinter()), nil
}

// republish re-snapshots and pushes after this machine changed.
func republish(ctx context.Context, s *session, out io.Writer) error {
	self, _, err := s.snapshot(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := s.store.Write(self); err != nil {
		return err
	}
	pushed, err := s.store.Publish(ctx, fmt.Sprintf("%s: after apply (%d formulae, %d casks)", self.Name, len(self.Formulae), len(self.Casks)))
	if err != nil {
		return err
	}
	if pushed {
		fmt.Fprintf(out, "%s republished %s\n", OkStyle.Render("✓"), AccentStyle.Render(self.Name))
	}
	return nil
}
