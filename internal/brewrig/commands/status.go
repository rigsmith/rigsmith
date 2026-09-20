package commands

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/plan"
	"github.com/rigsmith/rigsmith/internal/brewrig/store"
	"github.com/spf13/cobra"
)

// NewStatusCmd reports how this machine stands against the others.
func NewStatusCmd() *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what this machine is missing, what's outdated, and where versions differ",
		Long: "Three separate reports, because they have three different fixes:\n\n" +
			"  missing   in the union, not installed here      → brewrig apply\n" +
			"  outdated  a newer version exists upstream       → brewrig update\n" +
			"  skew      the machines are on different versions → brewrig update on the one behind\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd.Context(), cmd.OutOrStdout(), offline)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "don't contact the remote; report against the last pull")
	return cmd
}

func runStatus(ctx context.Context, out io.Writer, offline bool) error {
	s, err := open(ctx, !offline)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s %s\n", HeaderStyle.Render("machine"), AccentStyle.Render(s.cfg.Machine))
	if s.cfg.Remote != "" {
		reach := OkStyle.Render("reachable")
		if offline {
			reach = DimStyle.Render("not checked")
		} else if !store.Reachable(ctx, s.cfg.Remote) {
			reach = ErrStyle.Render("unreachable")
		}
		fmt.Fprintf(out, "  remote   %s  %s\n", s.cfg.Remote, reach)
	}
	if subject, when, ok := s.store.LastSync(ctx); ok {
		fmt.Fprintf(out, "  last     %s  %s\n", when, DimStyle.Render(subject))
	}

	all, err := s.store.Machines(ctx)
	if err != nil {
		return err
	}
	for _, m := range all {
		marker := " "
		if m.Name == s.cfg.Machine {
			marker = "*"
		}
		fmt.Fprintf(out, "  %s %-24s %3d formulae  %3d casks  %s\n",
			marker, m.Name, len(m.Formulae), len(m.Casks),
			DimStyle.Render(m.SyncedAt.Local().Format("2 Jan 15:04")))
	}
	if len(all) < 2 {
		fmt.Fprintf(out, "\n%s\n", DimStyle.Render("only one machine has published — run `brewrig init` on the other Mac"))
	}

	_, p, err := s.planNow(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	printPlan(out, p)

	outdated, err := s.brew.Outdated(ctx)
	if err != nil {
		return err
	}
	if len(outdated) > 0 {
		fmt.Fprintf(out, "\n%s (%d)\n", HeaderStyle.Render("outdated here"), len(outdated))
		// Homebrew routinely has dozens outdated at once, and a wall of names
		// buries the three reports above it — which are the ones that say
		// something about the other machine. The count is the actionable part;
		// `brew outdated` prints the full list.
		const show = 8
		for i, r := range outdated {
			if i == show {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf("… and %d more", len(outdated)-show)))
				break
			}
			fmt.Fprintf(out, "  %s %s\n", WarnStyle.Render("↑"), r.Label())
		}
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("run `brewrig update`"))
	}
	return nil
}

// printPlan renders the plan's three lists. Kept in one place so `sync`,
// `apply` and `status` describe the same situation the same way.
func printPlan(out io.Writer, p *plan.Plan) {
	if len(p.Install) > 0 {
		fmt.Fprintf(out, "%s (%d)\n", HeaderStyle.Render("missing here"), len(p.Install))
		for _, in := range p.Install {
			note := "on " + strings.Join(in.From, ", ")
			if in.Adopt {
				note += " · already here as a dependency"
			}
			fmt.Fprintf(out, "  %s %-32s %s\n", OkStyle.Render("+"), in.Ref.Label(), DimStyle.Render(note))
		}
	}
	if len(p.Taps) > 0 {
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("  taps to add:"), strings.Join(p.Taps, ", "))
	}
	if len(p.Remove) > 0 {
		fmt.Fprintf(out, "\n%s (%d)\n", HeaderStyle.Render("removed on another machine"), len(p.Remove))
		for _, rm := range p.Remove {
			fmt.Fprintf(out, "  %s %-32s %s\n", WarnStyle.Render("−"), rm.Ref.Label(),
				DimStyle.Render("by "+rm.By+" on "+rm.At.Local().Format("2 Jan")))
		}
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("`brewrig apply` asks about each one"))
	}
	if len(p.Skew) > 0 {
		fmt.Fprintf(out, "\n%s (%d)\n", HeaderStyle.Render("version skew"), len(p.Skew))
		for _, sk := range p.Skew {
			fmt.Fprintf(out, "  %s %-28s %s\n", WarnStyle.Render("≠"), sk.Ref.Label(),
				DimStyle.Render(versionSummary(sk)))
		}
	}
	if len(p.OptedOut) > 0 {
		fmt.Fprintf(out, "\n%s (%d)\n", HeaderStyle.Render("skipped on this machine"), len(p.OptedOut))
		for _, r := range p.OptedOut {
			fmt.Fprintf(out, "  %s %s\n", DimStyle.Render("·"), r.Label())
		}
	}
	if p.Empty() && len(p.Skew) == 0 {
		fmt.Fprintf(out, "%s in sync\n", OkStyle.Render("✓"))
	}
}

func versionSummary(sk plan.Skew) string {
	var parts []string
	for m, v := range sk.Versions {
		parts = append(parts, m+" "+v)
	}
	// Machine order varies by map iteration; sort so two runs read the same.
	sort.Strings(parts)
	return strings.Join(parts, "  ·  ")
}
