package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// wtBranchStyle paints a worktree's branch name in rig's core accent (blue), so
// the `list`/`-wt` output reads like the rest of rig rather than a wall of plain
// text. The selected and pinned rows override this with cyan/green.
var wtBranchStyle = lipgloss.NewStyle().Foreground(brandBlue)

// worktreesByRecent returns a copy of wts ordered newest-first by ModTime, so the
// worktree you last touched sits at the top. The sort is stable, so worktrees
// with an unknown (zero) time keep git's original order — which puts the main
// checkout first among them, as callers that index wts[0] still expect when they
// sort their own copy.
func worktreesByRecent(wts []gitrepo.Worktree) []gitrepo.Worktree {
	sorted := append([]gitrepo.Worktree(nil), wts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].ModTime.After(sorted[j].ModTime)
	})
	return sorted
}

// humanizeAgo renders a worktree's age as a short relative string ("just now",
// "5m", "3h", "2d", "6w", "1y") for the dim date column. An empty string is
// returned for a zero time so callers can omit the column when git gave us no
// date. now is passed in to keep the function pure and unit-testable.
func humanizeAgo(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}
