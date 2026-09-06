package bridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/contents"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// MinPruneDays is the closest to now this will fold history. Keeping a few days
// is what makes the recent activity feed's file lists readable, and a prune that
// left one commit standing would take the answer to "what did that sync do" away
// with it.
const MinPruneDays = 3

// RepoStats is the staging repo's size and shape, plus the one derived number
// worth acting on.
type RepoStats struct {
	gitrepo.Stats
	// Ratio is history over content. The checkout is what you are keeping; .git
	// is what it has cost to keep it. Transcripts are append-only and sync every
	// few minutes, so this climbs on its own and nobody notices until a push
	// gets slow.
	Ratio float64 `json:"ratio"`
	// Squashed says the oldest reachable commit is one a squash wrote, so First
	// is where history was truncated rather than where the repo began. Decided
	// here rather than in the window: which messages mean "squash" is a fact
	// about clauderig, not about a date picker.
	Squashed bool `json:"squashed"`
	// RetainedDays is how far back history actually goes. A date makes the
	// reader subtract it from today, and after a squash the answer is usually
	// "less than you think" — which is exactly when nobody does the arithmetic.
	RetainedDays float64 `json:"retainedDays"`
	// Contents is what the checkout is made of. A total says the repo is large
	// without saying what it is large WITH, and the two have different remedies:
	// transcripts answer to retention, attachments to the allowlist, history to
	// a prune. On a real repo 97% was conversation, which no squash touches.
	Contents []contents.Group `json:"contents,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// Repo backs the window's repository panel.
type Repo struct{}

// NewRepo builds the repo service.
func NewRepo() *Repo { return &Repo{} }

// Get reads the repo's numbers. Local only — no network, so it is safe beside
// the status poll.
func (s *Repo) Get(ctx context.Context) (RepoStats, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return RepoStats{Error: err.Error()}, nil
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return RepoStats{Error: "no staging repo yet — run `clauderig init`"}, nil
	}
	st, err := repo.Stats(ctx)
	if err != nil {
		return RepoStats{Error: err.Error()}, nil
	}
	out := RepoStats{Stats: st, Squashed: status.SquashedRoot(st.RootSubject)}
	// Best-effort: a breakdown that cannot be walked must not cost the numbers
	// above it, which are the ones the panel exists for.
	if rep, cerr := contents.Scan(staging); cerr == nil {
		out.Contents = rep.Fold().Groups
	}
	if !st.First.IsZero() {
		out.RetainedDays = time.Since(st.First).Hours() / 24
	}
	if st.WorkBytes > 0 {
		out.Ratio = float64(st.GitBytes) / float64(st.WorkBytes)
	}
	return out, nil
}

// PruneResult is what a prune actually did, in the terms the window reports it.
type PruneResult struct {
	Folded    int       `json:"folded"`
	Reclaimed int64     `json:"reclaimed"`
	Before    RepoStats `json:"before"`
	After     RepoStats `json:"after"`
}

// Prune folds history older than days into a single commit and force-pushes.
//
// The confirmation is the window's own dialog rather than the CLI's, which is
// why this calls the repo directly instead of shelling out: `clauderig repo
// prune` refuses to run without a terminal on purpose, and routing around that
// with a flag would remove the human from a decision that rewrites shared
// history. A modal the user clicked through IS that human.
func (s *Repo) Prune(ctx context.Context, days int) (PruneResult, error) {
	if days < MinPruneDays {
		return PruneResult{}, fmt.Errorf("keep at least %d days of history", MinPruneDays)
	}
	staging, err := config.StagingDir()
	if err != nil {
		return PruneResult{}, err
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return PruneResult{}, errors.New("no staging repo yet")
	}
	before, err := s.Get(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	folded, err := repo.SquashBefore(ctx, cutoff,
		"clauderig: history before "+cutoff.UTC().Format("2006-01-02"))
	if err != nil {
		return PruneResult{}, err
	}
	if folded == 0 {
		return PruneResult{Before: before, After: before}, nil
	}
	// The rewrite is local until this lands; a failure here leaves the machine
	// ahead of a remote that still has the old history, which the next sync
	// reports as a diverged push rather than silently papering over.
	if err := repo.ForcePush(ctx, "origin", "main"); err != nil {
		return PruneResult{}, fmt.Errorf("history folded locally, but the force-push failed: %w", err)
	}
	after, err := s.Get(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{
		Folded:    folded,
		Reclaimed: before.GitBytes - after.GitBytes,
		Before:    before,
		After:     after,
	}, nil
}

// Repack delta-compresses loose objects into the pack. No history is lost, which
// is why it needs no confirmation and why the window offers it first: a day of
// syncing lands undeltified, and append-only transcripts pack down to nearly
// nothing. Reaching for a prune before trying this trades history for something
// git gives back for free.
func (s *Repo) Repack(ctx context.Context) (PruneResult, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return PruneResult{}, err
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return PruneResult{}, errors.New("no staging repo yet")
	}
	before, err := s.Get(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	if err := repo.Repack(ctx); err != nil {
		return PruneResult{}, err
	}
	after, err := s.Get(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Reclaimed: before.GitBytes - after.GitBytes, Before: before, After: after}, nil
}
