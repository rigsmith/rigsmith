package commands

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
	"github.com/spf13/cobra"
)

// NewLedgerCmd builds the `ledger` command: inspect the permanent session index,
// and backfill it from git history for sessions that aged out before it existed.
func NewLedgerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Inspect the permanent session index (and backfill it from git history)",
		Long: "The synced repo is a rolling window — `sync` drops transcripts older than\n" +
			"retention.historyDays. The ledger is the part that isn't dropped: one row per\n" +
			"session (id, title, project, date), written before retention runs and kept\n" +
			"forever, so `search` can still name a chat whose body has aged out.\n\n" +
			"Bare `ledger` reports what it remembers; `ledger backfill` recovers rows for\n" +
			"sessions pruned before the ledger existed, reading them out of the sync repo's\n" +
			"git history.",
		RunE: func(cmd *cobra.Command, args []string) error { return runLedgerStatus(cmd) },
	}
	cmd.AddCommand(newLedgerBackfillCmd())
	return cmd
}

func runLedgerStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	staging, err := config.StagingDir()
	if err != nil {
		return err
	}
	rows := ledger.LoadAll(staging)
	fmt.Fprintln(out, HeaderStyle.Render("Session ledger"))
	if len(rows) == 0 {
		fmt.Fprintln(out, DimStyle.Render("  no rows yet — it fills on the next `clauderig sync`"))
		return nil
	}

	byDevice := map[string]int{}
	var oldest, newest time.Time
	for _, r := range rows {
		byDevice[r.RecordedBy]++
		if oldest.IsZero() || r.End.Before(oldest) {
			oldest = r.End
		}
		if r.End.After(newest) {
			newest = r.End
		}
	}
	fmt.Fprintf(out, "  %d session(s) remembered\n", len(rows))
	if !oldest.IsZero() {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
			"spanning %s → %s", oldest.Format("2006-01-02"), newest.Format("2006-01-02"))))
	}
	names := make([]string, 0, len(byDevice))
	for n := range byDevice {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		label := n
		if label == "" {
			label = "(unrecorded)"
		}
		fmt.Fprintf(out, "  %-24s %d\n", label, byDevice[n])
	}
	return nil
}

func newLedgerBackfillCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Recover ledger rows for sessions already pruned from the synced tree",
		Long: "A deleted file is gone from a git tree, not from history. `backfill` walks the\n" +
			"sync repo for transcripts retention has already removed, reads each one's head\n" +
			"from the commit before its deletion, and writes the row the ledger never got a\n" +
			"chance to write.\n\n" +
			"Rows already in the ledger are left alone — a live transcript is a better source\n" +
			"than a deleted blob. Run it once after adopting the ledger; there is no reason\n" +
			"to run it again, and running it twice does nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			repo, err := gitrepo.Open(ctx, staging)
			if err != nil {
				return fmt.Errorf("no sync repo to recover from: %w", err)
			}
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))

			l, err := ledger.Open(staging, me.Name)
			if err != nil {
				return err
			}
			before := l.Count()

			fmt.Fprintln(out, DimStyle.Render("reading deleted transcripts out of git history…"))
			res, err := ledger.Backfill(ctx, l, gitHistory{repo}, parseTranscriptHead)
			if err != nil {
				return err
			}
			if !dryRun {
				if err := l.Save(); err != nil {
					return err
				}
			}

			fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf(
				"%d recovered, %d already known, %d unreadable (of %d deleted transcripts)",
				res.Recovered, res.Skipped, res.Unreadable, res.Deleted)))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
				"ledger %d → %d session(s)", before, l.Count())))
			if dryRun {
				fmt.Fprintf(out, "%s\n", WarnStyle.Render("--dry-run: nothing written"))
			} else {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("commit it with your next `clauderig sync`"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "report what would be recovered without writing")
	return cmd
}

// gitHistory adapts the repo to the ledger's History port. The two Deletion
// types are deliberately separate: the ledger describes what it needs, and does
// not take a dependency on how git is driven.
type gitHistory struct{ repo *gitrepo.Repo }

func (g gitHistory) Deletions(ctx context.Context, pathspec string) ([]ledger.Deletion, error) {
	dels, err := g.repo.Deletions(ctx, pathspec)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Deletion, 0, len(dels))
	for _, d := range dels {
		out = append(out, ledger.Deletion{Path: d.Path, Commit: d.Commit})
	}
	return out, nil
}

func (g gitHistory) LastCommitTime(ctx context.Context, rev, path string) (time.Time, error) {
	return g.repo.LastCommitTime(ctx, rev, path)
}

func (g gitHistory) ShowPrefix(ctx context.Context, rev, path string, max int) ([]byte, error) {
	b, err := g.repo.ShowPrefix(ctx, rev, path, max)
	if err != nil {
		return nil, err
	}
	if transcript.IsIndex(b) {
		b, err = g.repo.ShowFile(ctx, rev, path)
		if err != nil {
			return nil, err
		}
	}
	return transcript.ReadStored(path, b, func(p string) ([]byte, error) { return g.repo.ShowFile(ctx, rev, p) }, int64(max))
}

// bytesReader is a fresh reader over the same head bytes — the two parsers each
// consume a stream, so they cannot share one.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// parseTranscriptHead pulls the title and cwd out of a transcript's leading
// bytes, using the same readers the live path uses so a recovered row is worded
// exactly like one written at sync time.
func parseTranscriptHead(head []byte) (title, cwd string) {
	title = session.FirstPromptFrom(bytesReader(head))
	if c, ok, err := project.CwdFrom(bytesReader(head)); err == nil && ok {
		cwd = c
	}
	return title, cwd
}
