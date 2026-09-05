package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/contents"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// NewRepoCmd is the staging repo itself: how big it has got, and how to make it
// smaller.
//
// The repo is the one part of clauderig that grows without anyone deciding it
// should. Transcripts are append-only and sync every few minutes, so each run
// stores another copy of a file that only got longer — history outgrows the
// content it is history OF, silently, and the first sign of it is a push getting
// slow. There is a size-triggered squash already, but it fires on a ratio nobody
// sees and answers a question nobody asked.
func NewRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Show the sync repo's size and history, or prune it",
		Long: "Reports what the staging repo costs: commits, tracked files, the size of\n" +
			"the checkout and the size of the history behind it.\n\n" +
			"`repo prune` folds history older than a cutoff into a single commit. The\n" +
			"files are untouched — only the steps that produced them go.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRepoStats(cmd.Context(), cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(newRepoGCCmd(), newRepoPruneCmd())
	return cmd
}

func runRepoStats(ctx context.Context, out io.Writer) error {
	staging, err := config.StagingDir()
	if err != nil {
		return err
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return fmt.Errorf("no staging repo yet — run `clauderig init`: %w", err)
	}
	s, err := repo.Stats(ctx)
	if err != nil {
		return err
	}
	printRepoStats(out, s)

	// The breakdown, not just the total: "1.6 GB" invites pruning history, and
	// on a real repo that would have been the wrong lever — nearly all of it was
	// conversation, which no squash touches.
	if rep, cerr := contents.Scan(staging); cerr == nil {
		printContents(out, rep.Fold())
	}
	return nil
}

func printContents(out io.Writer, rep contents.Report) {
	if len(rep.Groups) == 0 {
		return
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render("what it holds"))
	for _, g := range rep.Groups {
		share := ""
		if rep.Bytes > 0 {
			share = fmt.Sprintf("%3.0f%%", 100*float64(g.Bytes)/float64(rep.Bytes))
		}
		note := countOf(g.Files, "file", "files")
		if g.Name == "other" {
			note += " — " + g.Detail
		}
		fmt.Fprintf(out, "    %-26s %10s  %4s  %s\n",
			g.Name, humanBytes(g.Bytes), share, DimStyle.Render(note))
	}
	fmt.Fprintln(out)
}

func printRepoStats(out io.Writer, s gitrepo.Stats) {
	fmt.Fprintf(out, "\n  %-10s %s\n", "branch", s.Branch)
	fmt.Fprintf(out, "  %-10s %s across %s\n", "content", humanBytes(s.WorkBytes), countOf(s.Files, "file", "files"))
	fmt.Fprintf(out, "  %-10s %s in %s\n", "history", humanBytes(s.GitBytes), countOf(s.Commits, "commit", "commits"))

	// The ratio is the number that matters and the one nobody computes by eye.
	if s.WorkBytes > 0 {
		ratio := float64(s.GitBytes) / float64(s.WorkBytes)
		note := ""
		switch {
		case ratio >= 4:
			note = "  " + WarnStyle.Render("— history costs several times the content; `clauderig repo prune` reclaims it")
		case ratio >= 2:
			note = "  " + DimStyle.Render("— history is outgrowing the content")
		}
		fmt.Fprintf(out, "  %-10s %.1f× the content%s\n", "", ratio, note)
	}
	// Loose objects are not a long history and do not have a history's remedy.
	// Recent commits land undeltified, and append-only transcripts compress
	// enormously once packed — so this is the line to act on before anyone
	// considers throwing history away.
	if s.LooseBytes > 0 && s.GitBytes > 0 && float64(s.LooseBytes) > 0.4*float64(s.GitBytes) {
		fmt.Fprintf(out, "  %-10s %s in %s not yet packed %s\n", "",
			humanBytes(s.LooseBytes), countOf(s.LooseObjects, "object", "objects"),
			WarnStyle.Render("— `clauderig repo gc` reclaims it, keeping every commit"))
	}

	// The question this whole report exists to answer: how far back can I go?
	// A date alone does not answer it — you have to subtract it from today in
	// your head — and after a squash the honest answer is often "less than you
	// think", which is precisely when nobody does the subtraction.
	if !s.First.IsZero() {
		age := humanSpan(time.Since(s.First))
		if status.SquashedRoot(s.RootSubject) {
			fmt.Fprintf(out, "  %-10s %s %s\n", "retained", age,
				DimStyle.Render("— squashed "+s.First.Local().Format("2006-01-02 15:04")+", earlier history discarded"))
		} else {
			fmt.Fprintf(out, "  %-10s %s %s\n", "retained", age,
				DimStyle.Render("— since "+s.First.Local().Format("2006-01-02 15:04")))
		}
	}
	fmt.Fprintln(out)
}

// newRepoGCCmd is the remedy to reach for first, and the reason `prune` should
// rarely be needed: it costs no history at all.
func newRepoGCCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gc",
		Short: "Repack the sync repo, reclaiming space without losing any history",
		Long: "Delta-compresses loose objects into the pack and drops unreachable ones.\n\n" +
			"Every sync writes new objects loose and undeltified. Transcripts are\n" +
			"append-only, so each version is nearly the previous one and packs down to\n" +
			"almost nothing — but only once packed. Left alone, a day of syncing can\n" +
			"cost several times what the whole history costs after this runs.\n\n" +
			"Nothing reachable is lost. This is ordinary git maintenance, not a prune.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			repo, err := gitrepo.Open(cmd.Context(), staging)
			if err != nil {
				return fmt.Errorf("no staging repo yet — run `clauderig init`: %w", err)
			}
			was, err := repo.Stats(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\n  packing %s in %s…\n",
				humanBytes(was.LooseBytes), countOf(was.LooseObjects, "loose object", "loose objects"))
			if err := repo.Repack(cmd.Context()); err != nil {
				return fmt.Errorf("gc: %w", err)
			}
			now, err := repo.Stats(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "  %s reclaimed %s, every commit kept\n",
				OkStyle.Render("✓"), humanBytes(was.GitBytes-now.GitBytes))
			printRepoStats(out, now)
			return nil
		},
	}
}

func newRepoPruneCmd() *cobra.Command {
	var before string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Fold sync history older than a cutoff into one commit",
		Long: "Collapses every commit older than --before into a single base commit and\n" +
			"keeps the ones after it. The checkout does not change: the files stay\n" +
			"exactly as they are, and only the record of how they got there is dropped.\n\n" +
			"This rewrites the branch and force-pushes it. Other machines pick the new\n" +
			"history up on their next sync — the same thing the automatic size-based\n" +
			"squash already does.\n\n" +
			"What you lose is the ability to recover a session that retention deleted\n" +
			"before the cutoff: `clauderig ledger backfill` reads those out of history.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cutoff, err := pruneCutoff(before, time.Now())
			if err != nil {
				return fmt.Errorf("--before: %w", err)
			}

			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			repo, err := gitrepo.Open(cmd.Context(), staging)
			if err != nil {
				return fmt.Errorf("no staging repo yet — run `clauderig init`: %w", err)
			}
			was, err := repo.Stats(cmd.Context())
			if err != nil {
				return err
			}
			printRepoStats(out, was)

			// Almost all of .git being unpacked means the problem is not the
			// history's length, and throwing history away to fix it would be
			// giving up something for nothing.
			if was.LooseBytes > 0 && float64(was.LooseBytes) > 0.4*float64(was.GitBytes) {
				fmt.Fprintf(out, "  %s\n\n", WarnStyle.Render(
					"most of this is unpacked, not old — run `clauderig repo gc` first; it costs no history"))
			}

			// What the remote is at right now, checked BEFORE anything is
			// rewritten. Another machine that has pushed since this one last
			// fetched holds commits this history does not, and force-pushing
			// over them would delete them — silently, and from every machine.
			// The same sha becomes the lease on the push itself, so a machine
			// that pushes during the confirm prompt is rejected rather than
			// overwritten.
			remote := repo.HasRemote(cmd.Context(), "origin")
			tip := ""
			if remote {
				if err := repo.Fetch(cmd.Context(), "origin", "main"); err != nil {
					return fmt.Errorf("fetch before rewriting history: %w", err)
				}
				if tip, err = repo.RevParse(cmd.Context(), "origin/main"); err != nil {
					return fmt.Errorf("read origin/main: %w", err)
				}
				_, behind, known, aerr := repo.AheadBehind(cmd.Context(), "origin", "main")
				if aerr != nil {
					return aerr
				}
				if known && behind > 0 {
					return fmt.Errorf(
						"origin/main has %s this machine does not — run `clauderig sync` first, then prune",
						countOf(behind, "commit", "commits"))
				}
			}

			// Rewriting shared history is not something to do because a script
			// felt like it. Interactive only, and no --yes to route around it.
			if !Interactive() {
				return errors.New("refusing to rewrite history non-interactively; re-run in a terminal")
			}
			ok, err := confirmDestructive(fmt.Sprintf(
				"Fold everything before %s into one commit and force-push? The files are kept.",
				cutoff.Local().Format("Mon 2 Jan 2006")))
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted")
			}

			folded, err := repo.SquashBefore(cmd.Context(), cutoff,
				"clauderig: history before "+cutoff.UTC().Format("2006-01-02"))
			if err != nil {
				return fmt.Errorf("prune: %w", err)
			}
			if folded == 0 {
				fmt.Fprintln(out, DimStyle.Render("  nothing older than the cutoff — repo unchanged"))
				return nil
			}
			if remote {
				if err := repo.ForcePushWithLease(cmd.Context(), "origin", "main", tip); err != nil {
					return fmt.Errorf(
						"the history here was folded, but publishing it failed — another machine pushed in the meantime.\n"+
							"Run `clauderig sync` to take their work, then prune again: %w", err)
				}
			} else {
				fmt.Fprintln(out, DimStyle.Render("  no origin — folded locally only"))
			}

			now, err := repo.Stats(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "  %s folded %s, reclaimed %s\n", OkStyle.Render("✓"),
				countOf(folded, "commit", "commits"), humanBytes(was.GitBytes-now.GitBytes))
			printRepoStats(out, now)
			return nil
		},
	}
	cmd.Flags().StringVar(&before, "before", "30d",
		"fold everything before this date, or this age (2026-08-01, 7d, 90d)")
	return cmd
}

// humanBytes renders a size the way someone reading a report wants it, not the
// way a machine stores it.
func humanBytes(n int64) string {
	switch {
	case n < 0:
		return "0 B"
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// humanSpan renders a duration the way the question is asked — "do I have a
// month or a day?" — rather than in hours.
func humanSpan(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "under an hour"
	case d < 36*time.Hour:
		return countOf(int(d.Hours()+0.5), "hour", "hours")
	case d < 60*24*time.Hour:
		return countOf(int(d.Hours()/24+0.5), "day", "days")
	}
	return countOf(int(d.Hours()/24/30+0.5), "month", "months")
}

// pruneCutoff resolves --before to the start of a day.
//
// An age is a duration from right now, so "7d" would cut at whatever o'clock it
// happens to be — leaving a base commit at 08:18 on some Tuesday and a first
// kept commit eleven minutes later. That boundary means nothing to anyone
// reading it afterwards, and it makes two prunes a week apart incomparable.
// Snapping to local midnight makes the cut the thing people actually mean:
// everything before that date, whole days either side.
//
// A bare date is taken as-is (its own midnight), so `--before 2026-08-01` keeps
// all of August.
func pruneCutoff(spec string, now time.Time) (time.Time, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return time.Time{}, errors.New("give a date or an age, e.g. 2026-08-01 or 7d")
	}
	if d, err := time.ParseInLocation("2006-01-02", spec, time.Local); err == nil {
		return d, nil
	}
	age, err := sessions.ParseAge(spec)
	if err != nil {
		return time.Time{}, err
	}
	if age <= 0 {
		return time.Time{}, errors.New("must be a positive age, e.g. 7d")
	}
	return startOfDay(now.Add(-age)), nil
}

// startOfDay is local midnight — the repo is read by a person in their own
// timezone, not by a scheduler in UTC.
func startOfDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
