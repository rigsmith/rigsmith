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

			// A rejected push is reported by `sync` — to whichever channel it ran
			// on, which for the Stop hook is nobody. The state it leaves behind is
			// the durable evidence, so it is stated here, where people look.
			if info.Unpushed > 0 {
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render(fmt.Sprintf(
					"unpushed  %d commit(s) have never reached the remote", info.Unpushed)))
				if info.Unmerged > 0 {
					fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
						"          the remote also has %d this machine lacks — run `clauderig sync` in a terminal to reconcile", info.Unmerged)))
				} else {
					fmt.Fprintf(out, "  %s\n", DimStyle.Render(
						"          run `clauderig sync` to push them"))
				}
			} else if info.Unmerged > 0 {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
					"behind    %d commit(s) on the remote are not here yet — `clauderig pull`", info.Unmerged)))
			} else if info.HasStaging && !info.TrackingKnown && info.Remote != "" {
				// A remote is configured and has never been reached. Saying
				// "up to date with origin/main" here would be the same lie in a
				// different shape.
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render(
					"unpushed  never pushed to this remote"))
			}

			fmt.Fprintln(out, DimStyle.Render("  roots:"))
			w := 0
			for _, r := range info.Roots {
				w = rootColumn(w, r.ID)
			}
			for _, r := range info.Roots {
				if !r.Present {
					fmt.Fprintf(out, "  %-*s %s\n", w, r.ID, DimStyle.Render("absent here"))
					continue
				}
				fmt.Fprintf(out, "  %-*s %d files\n", w, r.ID, r.Files)
			}

			// Who this machine is logged in as. On a machine tracking several
			// accounts the live login CHANGES, and until something fails it is
			// invisible — which is exactly the state `status` should surface.
			printAccountLine(out, info.Account)

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
	line := clipWords(r.Summary(), lastRunMaxWidth)
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

// clipWords shortens s to at most n runes, ending on a word boundary where one
// is close by so the cut doesn't land mid-word. `clip` in recent.go is the
// plain rune truncation; this one is for prose that reads as a sentence.
func clipWords(s string, n int) string {
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
func humanizeSince(t time.Time) string { return humanizeSinceAt(t, time.Now()) }

// humanizeSinceAt is humanizeSince against an explicit now, so callers that
// already carry a clock (and tests) get a deterministic string.
func humanizeSinceAt(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
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

// printAccountLine renders the machine-wide Claude Code login.
func printAccountLine(out io.Writer, a status.AccountInfo) {
	switch {
	case a.LoggedOut:
		fmt.Fprintf(out, "  account   %s\n", DimStyle.Render("not logged in"))
		return
	case a.Problem != "":
		fmt.Fprintf(out, "  account   %s\n", WarnStyle.Render(a.Problem))
		return
	case a.Email == "":
		fmt.Fprintf(out, "  account   %s\n", DimStyle.Render("not logged in"))
		return
	}
	line := a.Email
	if a.Subscription != "" {
		line += DimStyle.Render(" · " + a.Subscription)
	}
	// The alias is the handle the user chose and will type; the account id is
	// just the slugified email, so printing it would only lengthen the line.
	if a.Alias != "" {
		line += DimStyle.Render(" · " + a.Alias)
	}
	if a.Desynced {
		// The desync `account doctor` exists to catch: requests authenticate as
		// one account while Claude Code displays another.
		line += "  " + ErrStyle.Render("✗ desynced — run `clauderig account doctor`")
	}
	fmt.Fprintf(out, "  account   %s\n", line)
	if a.Untracked {
		fmt.Fprintf(out, "            %s\n", DimStyle.Render(
			"not tracked by clauderig — `clauderig account add` to capture it"))
	}
	if a.PointerEmail != "" {
		// clauderig's arrow points elsewhere, so `account list` is describing a
		// different account than the one this machine authenticates as.
		fmt.Fprintf(out, "            %s\n", WarnStyle.Render(
			"clauderig's active pointer says "+a.PointerEmail+" — `clauderig account doctor`"))
	}
}

// rootColumn widens a root-id column to fit id, starting from the 8 the fixed
// "cli"/"desktop" layout used. A Desktop profile root is `desktop@<name>`, so a
// fixed column would ragged out the moment a profile is named anything but the
// shortest thing.
func rootColumn(w int, id string) int {
	if w < 8 {
		w = 8
	}
	if len(id) > w {
		return len(id)
	}
	return w
}
