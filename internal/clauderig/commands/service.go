package commands

import (
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

// applicationService keeps terminal formatting in the command layer. Progress
// arrives synchronously so messages retain their existing order around errors.
func applicationService(out io.Writer) service.Service {
	return service.Service{Observe: func(event service.Event) {
		switch e := event.(type) {
		case service.SyncStarted:
			fmt.Fprintln(out, HeaderStyle.Render("clauderig sync"))
		case service.IdentityRejected:
			fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
				"⚠ account identity not recorded: %s looks like %s — check what ~/.claude.json holds", e.Finding.Path, e.Finding.Kind)))
		case service.Captured:
			printSyncCapture(out, e.Report)
		case service.CaptureFailed:
			for _, f := range e.Findings {
				fmt.Fprintf(out, "  %s %s (%s)\n", ErrStyle.Render("LEAK"), f.Path, f.Kind)
			}
		case service.DryRunStaged:
			fmt.Fprintln(out, DimStyle.Render("\n  dry-run: staged + scanned, not committing"))
		case service.DeviceUnregistered:
			fmt.Fprintf(out, "  %s\n", WarnStyle.Render("machine name unresolved — syncing, but not registering this device"))
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

func printSyncCapture(out io.Writer, rep *engine.Report) {
	if rep != nil {
		w := 0
		for _, r := range rep.Roots {
			w = rootColumn(w, r.ID)
		}
		for _, r := range rep.Roots {
			if r.Skipped {
				fmt.Fprintf(out, "  %-*s %s\n", w, r.ID, DimStyle.Render("skipped (absent here)"))
				continue
			}
			extra := ""
			if r.Unchanged > 0 {
				extra += fmt.Sprintf(", %d unchanged", r.Unchanged)
			}
			if r.RetentionByAge > 0 {
				// "aged out" read as a deletion; nothing is deleted here. These
				// files stay in ~/.claude and are declined again every run.
				extra += fmt.Sprintf(", %d too old", r.RetentionByAge)
			}
			if r.SkippedFiles > 0 {
				extra += fmt.Sprintf(", %d skipped (churn)", r.SkippedFiles)
			}
			if r.Disallowed > 0 {
				extra += fmt.Sprintf(", %d no longer allowed", r.Disallowed)
			}
			if n := len(r.Oversize); n > 0 {
				extra += fmt.Sprintf(", %d too large", n)
			}
			if r.Deferred > 0 {
				extra += fmt.Sprintf(", %d large transcript(s) waiting for more content or to settle", r.Deferred)
			}
			fmt.Fprintf(out, "  %-*s %d files, %d secret field(s) redacted%s\n", w, r.ID, r.Files, r.Redactions, extra)
			// Name what was dropped for size — a silent cap reads as "everything
			// synced" when it didn't, and these are whole conversations.
			for _, o := range r.Oversize {
				fmt.Fprintf(out, "    %s %s\n", DimStyle.Render("too large:"),
					DimStyle.Render(fmt.Sprintf("%s (%s)", o.Rel, humanBytes(o.Bytes))))
			}
		}
		fmt.Fprintf(out, "  manifest  %d projects\n", rep.ManifestProjects)
		if rep.LedgerTotal > 0 {
			fmt.Fprintf(out, "  ledger    %d session(s) remembered (+%d)\n", rep.LedgerTotal, rep.LedgerAdded)
		}
		if rep.LedgerError != "" {
			fmt.Fprintf(out, "%s\n", WarnStyle.Render("  ledger    not updated: "+rep.LedgerError))
		}
		if rep.RetentionPruned > 0 {
			fmt.Fprintf(out, "  retention %d aged file(s) pruned from staging\n", rep.RetentionPruned)
		}
		if rep.SidecarsPruned > 0 {
			fmt.Fprintf(out, "  sidecars  %d orphaned session(s) pruned from staging\n", rep.SidecarsPruned)
		}
	}
}
