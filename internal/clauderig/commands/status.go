package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/health"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
	"github.com/spf13/cobra"
)

// NewStatusCmd builds the `status` command — a read-only summary of sync state:
// machine, remote reachability, last sync, per-root file counts, and hooks. Plain
// styled output (scriptable); the live view lives in `ui`.
func NewStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync state: remote, last sync, roots, hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, _ := config.StagingDir()
			settings, _ := settingsPath()
			info := status.Gather(ctx, cfg, me, staging, settings)

			if asJSON {
				// Deliberately skips the reachability probe: --json is for
				// scripts and pollers, and a hung remote must not hang them.
				return emitStatusJSON(out, info, staging)
			}

			fmt.Fprintln(out, HeaderStyle.Render("clauderig status"))
			fmt.Fprintf(out, "  machine   %s (%s)\n", info.Machine.Name, info.Machine.OS)

			if info.Remote == "" {
				fmt.Fprintf(out, "  remote    %s\n", DimStyle.Render("none configured — run `clauderig init`"))
			} else {
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				reach := gitrepo.Reachable(rctx, info.Remote)
				cancel()
				mark := ErrStyle.Render("unreachable")
				if reach {
					mark = OkStyle.Render("reachable")
				}
				fmt.Fprintf(out, "  remote    %s  %s\n", info.Remote, mark)
			}

			if info.LastSync != "" {
				fmt.Fprintf(out, "  last sync %s\n", info.LastSync)
			} else {
				fmt.Fprintf(out, "  last sync %s\n", DimStyle.Render("never (no staging repo yet)"))
			}
			if info.Dirty {
				fmt.Fprintf(out, "            %s\n", WarnStyle.Render("staging has uncommitted changes"))
			}
			fmt.Fprintf(out, "  vs remote %s\n", divergenceLine(info.Divergence))
			if line := lastRunLine(staging, info.Machine.Name); line != "" {
				fmt.Fprintf(out, "  last run  %s\n", line)
			}

			fmt.Fprintln(out, DimStyle.Render("  roots:"))
			for _, r := range info.Roots {
				if !r.Present {
					fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("absent here"))
					continue
				}
				fmt.Fprintf(out, "  %-8s %d files\n", r.ID, r.Files)
			}

			if len(info.Hooks) > 0 {
				fmt.Fprintf(out, "  hooks     %v\n", info.Hooks)
			} else {
				fmt.Fprintf(out, "  hooks     %s\n", DimStyle.Render("not installed (run `clauderig hooks install`)"))
			}

			if len(info.Devices) > 0 {
				fmt.Fprintln(out, DimStyle.Render("  devices:"))
				for _, d := range info.Devices {
					self := ""
					if d.Name == info.Machine.Name {
						self = DimStyle.Render(" (this)")
					}
					fmt.Fprintf(out, "  %-12s %s  %s%s\n", d.Name, d.OS,
						DimStyle.Render(humanizeSince(d.LastSync)), self)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the snapshot as JSON (for scripts and pollers)")
	return cmd
}

// StatusJSON is the `status --json` document. It embeds the gathered snapshot
// verbatim and adds the two things a caller would otherwise have to re-derive:
// the health verdict, and the last thing clauderig actually did.
//
// The plan's phrasing is the design here — "the UI reads the struct; --json
// marshals it" — so this stays a thin wrapper rather than a parallel shape that
// could drift from what the UI sees.
type StatusJSON struct {
	status.Info
	Level   string          `json:"level"`  // green | amber | red
	Reason  string          `json:"reason"` // stable token, e.g. "behind"
	Summary string          `json:"summary"`
	Action  string          `json:"action,omitempty"`
	LastRun *journal.Record `json:"lastRun,omitempty"`
}

func emitStatusJSON(out io.Writer, info status.Info, staging string) error {
	doc := StatusJSON{Info: info}

	if recs, err := journal.Read(staging, 1); err == nil && len(recs) > 0 {
		doc.LastRun = &recs[0]
	}
	var last journal.Record
	if doc.LastRun != nil {
		last = *doc.LastRun
	}
	rep := health.Of(info, last)
	doc.Level, doc.Reason, doc.Summary, doc.Action = rep.Level.String(), rep.Reason.String(), rep.Summary, rep.Action

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// divergenceLine renders how this machine sits against the remote. The
// 2026-08-07 incident is the reason this row exists: `last sync` reports the
// last *push*, so it read "5 minutes ago" while the repo sat 65 commits behind
// all day. Ordered worst-first so the state that needs a human wins the line.
func divergenceLine(d gitrepo.Divergence) string {
	switch {
	case d.Merging:
		return ErrStyle.Render("unresolved merge in staging — finish or abort it before syncing")
	case !d.Tracked:
		return DimStyle.Render("unknown — " + d.Ref + " not fetched yet")
	case d.Diverged() && d.Conflict:
		return ErrStyle.Render(fmt.Sprintf("diverged — %s, %s; a merge would conflict",
			commits(d.Ahead, "ahead"), commits(d.Behind, "behind")))
	case d.Diverged():
		return ErrStyle.Render(fmt.Sprintf("diverged — %s, %s; merges cleanly",
			commits(d.Ahead, "ahead"), commits(d.Behind, "behind")))
	case d.Behind > 0:
		return WarnStyle.Render(commits(d.Behind, "behind") + " — run `clauderig pull`")
	case d.Ahead > 0:
		return WarnStyle.Render(commits(d.Ahead, "ahead") + " — run `clauderig sync`")
	default:
		return OkStyle.Render("up to date")
	}
}

// lastRunLine renders the newest journal record — what actually happened the
// last time clauderig ran, on any machine. Before the journal, a failed sync
// left no trace here at all: the Stop hook's tripwire refusal ran for days
// while `status` reported a healthy last-push time. Returns "" when nothing has
// been recorded yet, so a fresh install shows no empty row.
//
// The text comes from journal.Record.Summary, the same string the UI's activity
// feed renders, so the two can't drift.
func lastRunLine(staging, thisMachine string) string {
	recs, err := journal.Read(staging, 1)
	if err != nil || len(recs) == 0 {
		return ""
	}
	r := recs[0]

	// Git failure text runs to several clauses of boilerplate; on a status
	// summary that wraps into a paragraph. Clip it here — the full text stays
	// in the journal and in the UI's feed, which wraps properly.
	line := clip(r.Summary(), lastRunMaxWidth)
	switch r.Outcome {
	case journal.OutcomeFailed:
		line = ErrStyle.Render(line)
	case journal.OutcomeRefused:
		line = WarnStyle.Render(line)
	}

	// Name the machine only when it isn't this one — on a single-machine setup
	// repeating the hostname on every line is noise.
	when := humanizeSince(r.At)
	if r.Machine != "" && r.Machine != thisMachine {
		when = r.Machine + ", " + when
	}
	return line + " " + DimStyle.Render("("+when+")")
}

// lastRunMaxWidth keeps the `last run` row to roughly one terminal line once
// the "  last run  " label and the trailing "(when)" are accounted for.
const lastRunMaxWidth = 96

// clip shortens s to at most n runes, ending on a word boundary where one is
// close by so the cut doesn't land mid-word.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// commits renders "1 commit behind" / "65 commits behind".
func commits(n int, direction string) string {
	if n == 1 {
		return "1 commit " + direction
	}
	return fmt.Sprintf("%d commits %s", n, direction)
}

// humanizeSince renders a coarse relative time for device last-sync display.
func humanizeSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
