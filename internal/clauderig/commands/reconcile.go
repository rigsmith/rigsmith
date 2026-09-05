package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

// resolvedListLimit caps how many merged paths are named before the rest are
// summarised.
const resolvedListLimit = 20

// reconcile brings the staging repo back onto one line of history with the
// remote, and is safe to call when a previous run already left a merge in
// progress — that repair is the point. A staging repo abandoned mid-merge is not
// self-healing: the ff-only pull the SessionStart hook runs fails with "unmerged
// files" every session afterwards, so the wedge outlives whatever caused it until
// someone opens the repo by hand.
//
// Conflicts go through clauderig's policies (see internal/clauderig/mergepolicy)
// rather than to a human, because the merge usually happens where no human is
// watching. Anything the policies decline is handed to git mergetool only when
// allowMergeTool says the caller can afford to block on a person — `sync` can,
// the SessionStart hook cannot. Otherwise the merge is aborted, which leaves the
// repo usable even though the sync did not land.
func reconcile(ctx context.Context, out io.Writer, repo *gitrepo.Repo, remote, branch string, allowMergeTool bool) error {
	if !repo.InMerge(ctx) {
		conflicted, err := repo.FetchMerge(ctx, remote, branch)
		if err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
		if !conflicted {
			return nil
		}
	} else {
		fmt.Fprintln(out, DimStyle.Render("  finishing a merge left in progress by an earlier run…"))
	}

	rep, err := mergepolicy.Resolve(ctx, repo)
	if err != nil {
		return fmt.Errorf("resolve conflicts: %w", err)
	}
	// Name what was merged rather than just counting it — these are the user's
	// transcripts and notes, and a silent "resolved 300 files" is exactly the kind
	// of reassurance that hides a bad policy. Bounded, though: this also prints
	// from the SessionStart hook, where a long divergence would otherwise bury the
	// start of the session.
	for i, r := range rep.Resolved {
		if i == resolvedListLimit {
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf("…and %d more", len(rep.Resolved)-i)))
			break
		}
		fmt.Fprintf(out, "  %s %s %s\n",
			DimStyle.Render("merged"), r.Path, DimStyle.Render("("+string(r.Policy)+": "+r.Note+")"))
	}
	if n := len(rep.Unresolved); n > 0 {
		if !allowMergeTool || !interactive() {
			_ = repo.AbortMerge(ctx)
			return fmt.Errorf("%d conflict(s) need a human (%s); re-run `clauderig sync` in a terminal to resolve via git mergetool",
				n, rep.Unresolved[0])
		}
		fmt.Fprintln(out, WarnStyle.Render(fmt.Sprintf("  %d conflict(s) need you — launching git mergetool…", n)))
		if err := repo.RunMergeTool(ctx); err != nil {
			_ = repo.AbortMerge(ctx)
			return fmt.Errorf("mergetool: %w", err)
		}
	}
	root, err := repo.Toplevel(ctx)
	if err != nil {
		return err
	}
	if err = engine.CheckPublish(root); err != nil {
		return err
	}
	return repo.CommitMerge(ctx)
}

// repairWedgedMerge finishes a merge an earlier run left in progress, before
// anything else touches the staging repo. Order matters: sync commits by staging
// the whole tree, and `git add -A` over a conflicted tree marks the conflicts
// resolved with their `<<<<<<<` markers still in the files — so a repo left
// mid-merge does not just block the next sync, it is one commit away from
// publishing corrupted transcripts and settings to every other machine. Repairing
// first makes that unreachable.
//
// It reports whether the repo is safe to write into afterwards. Callers that go
// on to STAGE and COMMIT must stop when it is not: `git add -A` over a still
// conflicted index marks the conflicts resolved with their markers intact, so
// continuing would publish exactly what this function exists to prevent — and a
// failure here does not always end in an abort (a failed CommitMerge leaves the
// merge standing). The SessionStart path writes nothing, so it can ignore the
// result and degrade to "sync is behind" rather than "the session will not
// start".
func repairWedgedMerge(ctx context.Context, out io.Writer, staging string, allowMergeTool bool) (safe bool) {
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return true // no staging repo yet — nothing to wedge
	}
	if !repo.InMerge(ctx) {
		return true
	}
	if err := reconcile(ctx, out, repo, "origin", "main", allowMergeTool); err != nil {
		fmt.Fprintf(out, "clauderig: staging repo is mid-merge and could not be settled automatically: %v\n", err)
	}
	return !repo.InMerge(ctx)
}
