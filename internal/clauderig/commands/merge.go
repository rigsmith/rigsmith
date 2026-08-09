package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/merge"
	"github.com/spf13/cobra"
)

// NewMergeCmd builds the `merge` command — reconcile a diverged staging repo by
// applying clauderig's resolution policies.
//
// `pull` is fast-forward-only, so once both machines have moved it simply fails,
// which is how a 65-commit divergence stayed invisible for a day in August 2026.
// Resolving it by hand took an afternoon, and every conflict turned out to have a
// mechanical answer. This is that afternoon, encoded. Files matching no policy
// are left conflicted and named, never resolved by guesswork.
func NewMergeCmd() *cobra.Command {
	var abort bool
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Reconcile a diverged sync repo using clauderig's merge policies",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
				return fmt.Errorf("no staging repo yet — run `clauderig sync` or `clauderig init` first")
			}
			repo, err := gitrepo.Open(ctx, staging)
			if err != nil {
				return err
			}

			fmt.Fprintln(out, HeaderStyle.Render("clauderig merge"))

			if abort {
				if err := repo.AbortMerge(ctx); err != nil {
					return fmt.Errorf("abort: %w", err)
				}
				fmt.Fprintln(out, OkStyle.Render("  ✓ merge aborted; the repo is back where it was"))
				return nil
			}

			// Resume a merge already in progress rather than starting another —
			// a half-finished merge is exactly the state someone runs this in.
			resuming := false
			if unmerged, _ := repo.UnmergedFiles(ctx); len(unmerged) > 0 {
				resuming = true
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("resuming the merge already in progress"))
			}

			if !resuming {
				if cfg.Remote == "" {
					return fmt.Errorf("no remote configured — run `clauderig init` first")
				}
				if err := repo.SetRemote(ctx, "origin", cfg.Remote); err != nil {
					return err
				}
				if err := repo.Fetch(ctx, "origin", "main"); err != nil {
					return fmt.Errorf("fetch: %w", err)
				}
				d, err := repo.DivergenceFrom(ctx, "origin/main")
				if err != nil {
					return err
				}
				if d.Behind == 0 {
					fmt.Fprintln(out, OkStyle.Render("  ✓ nothing to merge — already up to date with the remote"))
					return nil
				}
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
					"merging origin/main — %s, %s", commits(d.Ahead, "ahead"), commits(d.Behind, "behind"))))

				conflicted, err := repo.MergeRef(ctx, "origin/main")
				if err != nil {
					return err
				}
				if !conflicted {
					fmt.Fprintln(out, OkStyle.Render("  ✓ merged cleanly — no policies needed"))
					_ = journal.Append(staging, journal.Succeeded(me.Name, journal.OpMerge))
					return nil
				}
			}

			resolved, residual, err := applyPolicies(ctx, out, repo)
			if err != nil {
				return err
			}

			if len(residual) > 0 {
				// Leave the repo mid-merge on purpose: the user's own mergetool
				// is the right tool for a file we have no policy for, and
				// aborting would throw away the work already resolved.
				fmt.Fprintf(out, "\n  %s\n", ErrStyle.Render(fmt.Sprintf(
					"%d file(s) match no policy and need you:", len(residual))))
				for _, p := range residual {
					fmt.Fprintf(out, "    %s\n", p)
				}
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(
					"Resolve them, `git -C "+staging+" commit`, or run `clauderig merge --abort`."))
				err := fmt.Errorf("%d unresolved conflict(s)", len(residual))
				_ = journal.Append(staging, journal.Failed(me.Name, journal.OpMerge, err))
				return err
			}

			if err := repo.CommitMerge(ctx); err != nil {
				return fmt.Errorf("commit merge: %w", err)
			}
			fmt.Fprintf(out, "\n  %s\n", OkStyle.Render(fmt.Sprintf(
				"✓ merged — %d file(s) resolved by policy", resolved)))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("Run `clauderig sync` to push the result."))

			rec := journal.Succeeded(me.Name, journal.OpMerge)
			rec.Files = resolved
			_ = journal.Append(staging, rec)
			return nil
		},
	}
	cmd.Flags().BoolVar(&abort, "abort", false, "back out an in-progress merge, restoring the pre-merge state")
	return cmd
}

// applyPolicies resolves every conflicted file it has a policy for, printing the
// per-file ledger as it goes, and returns the paths it could not handle.
//
// The ledger is the point: a merge that silently rewrites a day of conversation
// history is not something anyone should have to trust blindly.
func applyPolicies(ctx context.Context, out io.Writer, repo *gitrepo.Repo) (resolved int, residual []string, err error) {
	unmerged, err := repo.UnmergedFiles(ctx)
	if err != nil {
		return 0, nil, err
	}
	sort.Strings(unmerged)

	for _, rel := range unmerged {
		base, ours, theirs, err := repo.ConflictStages(ctx, rel)
		if err != nil {
			return 0, nil, err
		}

		res, ok := merge.Resolve(merge.Sides{Path: rel, Base: base, Ours: ours, Theirs: theirs})
		if !ok {
			residual = append(residual, rel)
			continue
		}

		dst := filepath.Join(repo.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, nil, err
		}
		if err := os.WriteFile(dst, res.Content, 0o644); err != nil {
			return 0, nil, err
		}
		if err := repo.AddPaths(ctx, rel); err != nil {
			return 0, nil, err
		}

		fmt.Fprintf(out, "  %s %s\n", OkStyle.Render("✓"), rel)
		fmt.Fprintf(out, "    %s %s\n", DimStyle.Render(res.Policy+":"), DimStyle.Render(res.Detail))
		resolved++
	}
	return resolved, residual, nil
}
