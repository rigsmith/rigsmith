package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/spf13/cobra"
)

// newDesktopPruneCmd reclaims disk from a profile without deleting it. The
// tiers are ordered by what they cost the user: caches lose nothing, the VM
// image loses whatever was created inside Cowork and never exported, the whole
// bundle loses a download. Only the first runs without asking.
func newDesktopPruneCmd() *cobra.Command {
	var vm, all, dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "prune [<name|email>]",
		Short: "Reclaim a profile's disk space — caches, and optionally the Cowork VM image — without deleting it",
		Long: "Frees space a profile is holding while leaving its login and chat history\n" +
			"untouched. Nearly all of a large profile is the Cowork local-agent VM: its\n" +
			"unpacked root filesystem is sparse, provisioned at ~10 GB, and only ever\n" +
			"grows — deleting files inside the VM never shrinks it on the host.\n\n" +
			"What each tier reclaims:\n\n" +
			"  (default)  Electron caches (Cache, Code Cache, GPUCache, Dawn*Cache).\n" +
			"             Regenerated as needed; nothing is lost.\n" +
			"  --vm       also the unpacked VM root filesystem (rootfs.img). Desktop\n" +
			"             re-extracts it from the compressed image beside it on next\n" +
			"             launch, so the VM resets to pristine: anything created inside\n" +
			"             Cowork that was never exported to the host is gone.\n" +
			"  --all      also the compressed image, kernel and initramfs — the whole\n" +
			"             bundle — accepting a re-download on next launch.\n\n" +
			"`--dry-run` prints the per-profile breakdown and deletes nothing. With no\n" +
			"profile named, every profile is measured (and, without --dry-run, pruned).\n" +
			"A profile whose window is open is refused: Desktop writes into these\n" +
			"directories while it runs. `--vm` and `--all` ask first on a terminal and\n" +
			"need `--yes` off one; the default tier never asks.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			tier := desktop.PruneCaches
			switch {
			case all:
				tier = desktop.PruneAll
			case vm:
				tier = desktop.PruneVM
			}
			st, err := desktopStore()
			if err != nil {
				return err
			}
			var targets []desktop.Profile
			if len(args) > 0 {
				p, rerr := st.Resolve(args[0])
				if rerr != nil {
					return desktopNotFound(rerr, args[0])
				}
				targets = []desktop.Profile{p}
			} else if targets, err = st.List(); err != nil {
				return err
			}
			if len(targets) == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"no Desktop profiles yet — `clauderig desktop add <name>` creates one"))
				return nil
			}

			usages := make([]desktop.Usage, len(targets))
			var reclaim int64
			for i, p := range targets {
				u, merr := desktop.Measure(p)
				if merr != nil {
					return fmt.Errorf("measuring %s: %w", p.Name, merr)
				}
				usages[i] = u
				reclaim += u.Reclaimable(tier)
				printPruneBreakdown(out, p, u, tier)
			}
			if dryRun {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
					"dry run — would reclaim %s (%s); nothing deleted", desktop.HumanSize(reclaim), tier)))
				return nil
			}
			if reclaim == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to reclaim at this tier"))
				return nil
			}

			// The default tier loses nothing, so it needs no confirmation. The
			// others lose something the user cannot get back, so they are named
			// before anything is deleted — and off a terminal only an explicit
			// --yes stands in for the answer.
			if tier > desktop.PruneCaches && !yes {
				if !Interactive() {
					return fmt.Errorf("--%s discards data — pass --yes to confirm off a terminal", tier)
				}
				title := fmt.Sprintf("Reset the Cowork VM to pristine? Anything created inside it and never exported to the host is lost (%s).",
					desktop.HumanSize(reclaim))
				if tier == desktop.PruneAll {
					title = fmt.Sprintf("Delete the whole Cowork VM bundle? Everything created inside the VM is lost and Desktop re-downloads the image next launch (%s).",
						desktop.HumanSize(reclaim))
				}
				ok, cerr := confirmDestructive(title)
				if cerr != nil {
					return cerr
				}
				if !ok {
					fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing deleted"))
					return nil
				}
			}

			app := newDesktopApp()
			// Every profile is checked before any is touched: with several to
			// prune, an open one found after the first had been reset would
			// leave the command failing halfway through irreversible work.
			// Same rule as rm: unknown counts as open. The VM image may be
			// mounted by a running Cowork agent, and the caches are being
			// written to — deleting either under a live app is a race.
			for i, p := range targets {
				if usages[i].Reclaimable(tier) == 0 {
					continue
				}
				running, rerr := desktop.IsRunning(app, p.DataDir())
				if rerr != nil {
					return fmt.Errorf("could not tell whether %s is open: %w\n"+
						"Refusing to prune a profile that may still be running — close Claude Desktop and retry", p.Name, rerr)
				}
				if running {
					return fmt.Errorf("%s is open — close it first with `clauderig desktop quit %s`; nothing was deleted", p.Name, p.Name)
				}
			}
			var freed int64
			for i, p := range targets {
				if usages[i].Reclaimable(tier) == 0 {
					continue
				}
				removed, perr := desktop.Prune(p, tier)
				for _, e := range removed {
					freed += e.Size
				}
				if perr != nil {
					return fmt.Errorf("pruning %s (reclaimed %s before the error): %w", p.Name, desktop.HumanSize(freed), perr)
				}
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ reclaimed"), desktop.HumanSize(freed))
			if tier > desktop.PruneCaches {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("  the Cowork VM rebuilds on its next launch; logins and chat history are untouched"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&vm, "vm", false, "also drop the unpacked Cowork VM image (re-extracted on next launch; VM contents lost)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "also drop the whole VM bundle (re-downloaded on next launch)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the per-profile breakdown and delete nothing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation --vm and --all ask for")
	return cmd
}

// printPruneBreakdown prints one profile's total and each reclaimable entry,
// marking the ones the chosen tier leaves alone with the flag that would take
// them — so the numbers are visible before, and regardless of, any deletion.
func printPruneBreakdown(out io.Writer, p desktop.Profile, u desktop.Usage, tier desktop.PruneTier) {
	fmt.Fprintf(out, "%s  %s\n", HeaderStyle.Render(p.Label()), DimStyle.Render(desktop.HumanSize(u.Total)+" on disk"))
	if len(u.Entries) == 0 {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("nothing reclaimable"))
		return
	}
	width := 0
	for _, e := range u.Entries {
		width = max(width, len(e.Rel))
	}
	for _, e := range u.Entries {
		size := fmt.Sprintf("%9s", desktop.HumanSize(e.Size))
		line := fmt.Sprintf("  %-*s  %s", width, e.Rel, size)
		switch {
		case e.Tier <= tier:
			line += "  " + DimStyle.Render(e.Note)
		default:
			line = DimStyle.Render(line + "  kept — needs --" + e.Tier.String())
		}
		fmt.Fprintln(out, line)
	}
	if kept := u.Reclaimable(desktop.PruneAll) - u.Reclaimable(tier); kept > 0 {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(strings.TrimSpace(
			fmt.Sprintf("%s more reclaimable at a higher tier", desktop.HumanSize(kept)))))
	}
}
