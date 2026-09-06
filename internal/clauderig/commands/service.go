package commands

import (
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

// applicationService keeps terminal formatting in the command layer. Progress
// arrives synchronously so messages retain their existing order around errors.
func applicationService(out io.Writer) service.Service {
	return service.Service{Observe: func(event service.Event) {
		switch e := event.(type) {
		case service.MergePending:
			fmt.Fprintln(out, DimStyle.Render("  finishing a merge left in progress by an earlier run…"))
		case service.ConflictsResolved:
			const resolvedListLimit = 20
			for i, r := range e.Resolutions {
				if i == resolvedListLimit {
					fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf("…and %d more", len(e.Resolutions)-i)))
					break
				}
				fmt.Fprintf(out, "  %s %s %s\n", DimStyle.Render("merged"), r.Path,
					DimStyle.Render("("+string(r.Policy)+": "+r.Note+")"))
			}
		case service.MergeToolStarting:
			fmt.Fprintln(out, WarnStyle.Render(fmt.Sprintf("  %d conflict(s) need you — launching git mergetool…", e.Count)))
		case service.MergeRepairFailed:
			fmt.Fprintf(out, "clauderig: staging repo is mid-merge and could not be settled automatically: %v\n", e.Err)
		case service.Published:
			switch {
			case e.LocalOnly && e.Result.Committed:
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ committed locally (no remote — run init)"))
			case e.LocalOnly:
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ already up to date (no remote)"))
			case e.Result.Committed:
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ synced & pushed"))
			default:
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ in sync"))
			}
		case service.Repacking:
			fmt.Fprintf(out, "  %s repacking (.git %dMB > %.0f× worktree)\n", DimStyle.Render("⟳"), e.GitBytes>>20, e.Factor)
		case service.HistoryFolded:
			fmt.Fprintf(out, "  %s folded %d commit(s) before %s, kept the last %d days\n", DimStyle.Render("⟳"), e.Count, e.Cutoff.Format("2006-01-02"), e.KeepDays)
		case service.CloneSkipped:
			fmt.Fprintf(out, "clauderig pull: clone skipped: %v\n", e.Err)
		case service.PullFailed:
			fmt.Fprintf(out, "clauderig pull: %v\n", e.Err)
		case service.AutoRestored:
			fmt.Fprintln(out, "clauderig: fresh machine — auto-restored from sync")
			if e.DesktopSessions > 0 {
				printDesktopRestartNudge(out, e.DesktopSessions)
			}
		}
	}}
}
