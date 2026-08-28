package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
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
	cmd.AddCommand(newRepoPruneCmd())
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
	return nil
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
	if !s.First.IsZero() {
		fmt.Fprintf(out, "  %-10s %s → %s\n", "spanning",
			s.First.Local().Format("2006-01-02"), s.Last.Local().Format("2006-01-02"))
	}
	fmt.Fprintln(out)
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
			age, err := sessions.ParseAge(before)
			if err != nil {
				return fmt.Errorf("--before: %w", err)
			}
			if age <= 0 {
				return errors.New("--before must be a positive age, e.g. 7d")
			}
			cutoff := time.Now().Add(-age)

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

			// Rewriting shared history is not something to do because a script
			// felt like it. Interactive only, and no --yes to route around it.
			if !Interactive() {
				return errors.New("refusing to rewrite history non-interactively; re-run in a terminal")
			}
			ok, err := confirmDestructive(fmt.Sprintf(
				"Fold history before %s into one commit and force-push? The files are kept.",
				cutoff.Local().Format("2006-01-02 15:04")))
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
			if err := repo.ForcePush(cmd.Context(), "origin", "main"); err != nil {
				return fmt.Errorf("force-push after prune: %w", err)
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
		"fold history older than this age (e.g. 7d, 90d)")
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
