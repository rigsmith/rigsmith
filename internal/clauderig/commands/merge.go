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
	var abort, asJSON bool
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

			// Under --json stdout carries the document and nothing else.
			say := func(format string, a ...any) {
				if !asJSON {
					fmt.Fprintf(out, format, a...)
				}
			}
			doc := MergeJSON{}

			say("%s\n", HeaderStyle.Render("clauderig merge"))

			if abort {
				if err := repo.AbortMerge(ctx); err != nil {
					return fmt.Errorf("abort: %w", err)
				}
				say("%s\n", OkStyle.Render("  ✓ merge aborted; the repo is back where it was"))
				if asJSON {
					return emitJSON(out, doc)
				}
				return nil
			}

			// Resume a merge already in progress rather than starting another —
			// a half-finished merge is exactly the state someone runs this in.
			//
			// Keyed on MERGE_HEAD, not on unresolved files. Someone who has
			// resolved every conflict but not yet committed is still mid-merge,
			// and starting a fresh fetch and merge there is something git
			// refuses outright.
			resuming := repo.InMerge(ctx)
			if resuming {
				say("  %s\n", DimStyle.Render("resuming the merge already in progress"))
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
				doc.Ahead, doc.Behind = d.Ahead, d.Behind
				if d.Behind == 0 {
					doc.NothingToDo, doc.Merged = true, true
					say("%s\n", OkStyle.Render("  ✓ nothing to merge — already up to date with the remote"))
					if asJSON {
						return emitJSON(out, doc)
					}
					return nil
				}
				say("  %s\n", DimStyle.Render(fmt.Sprintf(
					"merging origin/main — %s, %s", commits(d.Ahead, "ahead"), commits(d.Behind, "behind"))))

				conflicted, err := repo.MergeRef(ctx, "origin/main")
				if err != nil {
					return err
				}
				if !conflicted {
					doc.Merged = true
					say("%s\n", OkStyle.Render("  ✓ merged cleanly — no policies needed"))
					_ = journal.Append(staging, journal.Succeeded(me.Name, journal.OpMerge))
					if asJSON {
						return emitJSON(out, doc)
					}
					return nil
				}
			}

			ledger, residual, err := applyPolicies(ctx, repo)
			if err != nil {
				return err
			}
			doc.Resolved, doc.Residual = ledger, residual
			if !asJSON {
				printLedger(out, ledger)
			}

			if len(residual) > 0 {
				// Leave the repo mid-merge on purpose: the user's own mergetool
				// is the right tool for a file we have no policy for, and
				// aborting would throw away the work already resolved.
				say("\n  %s\n", ErrStyle.Render(fmt.Sprintf(
					"%d file(s) match no policy and need you:", len(residual))))
				for _, p := range residual {
					say("    %s\n", p)
				}
				say("  %s\n", DimStyle.Render(
					"Resolve them, `git -C "+staging+" commit`, or run `clauderig merge --abort`."))
				err := fmt.Errorf("%d unresolved conflict(s)", len(residual))
				_ = journal.Append(staging, journal.Failed(me.Name, journal.OpMerge, err))
				if asJSON {
					// The residual list IS the answer here, so emit the document
					// and still exit nonzero — a caller checking only the exit
					// code must not read this as success.
					doc.Error = err.Error()
					if e := emitJSON(out, doc); e != nil {
						return e
					}
				}
				return err
			}

			if err := repo.CommitMerge(ctx); err != nil {
				return fmt.Errorf("commit merge: %w", err)
			}
			doc.Merged = true
			say("\n  %s\n", OkStyle.Render(fmt.Sprintf(
				"✓ merged — %d file(s) resolved by policy", len(ledger))))
			say("  %s\n", DimStyle.Render("Run `clauderig sync` to push the result."))

			rec := journal.Succeeded(me.Name, journal.OpMerge)
			rec.Files = len(ledger)
			_ = journal.Append(staging, rec)
			if asJSON {
				return emitJSON(out, doc)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&abort, "abort", false, "back out an in-progress merge, restoring the pre-merge state")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the resolution ledger as JSON")
	return cmd
}

// Resolution is one file the merge dealt with — what ran, and what it did.
// This is the ledger the UI's Resolve panel renders and `--json` emits.
type Resolution struct {
	Path   string `json:"path"`
	Policy string `json:"policy"`
	Detail string `json:"detail"`
}

// MergeJSON is the `merge --json` document. Residual conflicts are a first-class
// field, not an error string: they are the merge's most important output, and a
// caller has to be able to enumerate them.
type MergeJSON struct {
	Merged      bool         `json:"merged"`
	Resolved    []Resolution `json:"resolved"`
	Residual    []string     `json:"residual"`
	Ahead       int          `json:"ahead"`
	Behind      int          `json:"behind"`
	NothingToDo bool         `json:"nothingToDo"`
	Error       string       `json:"error,omitempty"`
}

// applyPolicies resolves every conflicted file it has a policy for, returning
// the ledger and the paths it could not handle. Rendering is the caller's job,
// so the styled output and --json describe one set of facts.
//
// The ledger is the point: a merge that silently rewrites a day of conversation
// history is not something anyone should have to trust blindly.
func applyPolicies(ctx context.Context, repo *gitrepo.Repo) (ledger []Resolution, residual []string, err error) {
	unmerged, err := repo.UnmergedFiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(unmerged)

	for _, rel := range unmerged {
		base, ours, theirs, err := repo.ConflictStages(ctx, rel)
		if err != nil {
			return nil, nil, err
		}

		res, ok := merge.Resolve(merge.Sides{Path: rel, Base: base, Ours: ours, Theirs: theirs})
		if !ok {
			residual = append(residual, rel)
			continue
		}

		dst := filepath.Join(repo.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, err
		}
		// The conflicted path was checked out from a repo another machine
		// wrote. A symlink there would have this resolution written through it,
		// landing wherever it points — outside the staging tree entirely.
		if info, lerr := os.Lstat(dst); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("%s is a symlink — refusing to write a resolution through it", rel)
		}
		if err := os.WriteFile(dst, res.Content, 0o644); err != nil {
			return nil, nil, err
		}
		if err := repo.AddPaths(ctx, rel); err != nil {
			return nil, nil, err
		}
		ledger = append(ledger, Resolution{Path: rel, Policy: res.Policy, Detail: res.Detail})
	}
	return ledger, residual, nil
}

// printLedger renders the ledger the way the terminal wants it.
func printLedger(out io.Writer, ledger []Resolution) {
	for _, r := range ledger {
		fmt.Fprintf(out, "  %s %s\n", OkStyle.Render("✓"), r.Path)
		fmt.Fprintf(out, "    %s %s\n", DimStyle.Render(r.Policy+":"), DimStyle.Render(r.Detail))
	}
}
