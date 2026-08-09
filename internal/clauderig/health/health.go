// Package health reduces a status snapshot to the one thing the ambient UI
// shows: is sync fine, does it need a nudge, or is it stuck? The tray colour,
// the window banner, and any future embedded readout all call Of, so they can
// never disagree about what "synced" means.
//
// The three levels come straight from the 2026-08-07 divergence incident: the
// machine that sat 65 commits behind for a day reported "synced 5 minutes ago",
// because every surface keyed off the last *push*. Level keys off the repo's
// actual position instead.
package health

import (
	"fmt"

	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// Level is the tray colour: three states, deliberately.
type Level int

const (
	// Green — level with the remote, nothing to do.
	Green Level = iota
	// Amber — drifting but self-healing: one `pull` or `sync` fixes it.
	Amber
	// Red — stuck: needs a merge or a decision, and will not fix itself.
	Red
)

func (l Level) String() string {
	switch l {
	case Green:
		return "green"
	case Amber:
		return "amber"
	case Red:
		return "red"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Reason names *why* a level was chosen, so the UI can branch on a value rather
// than parse Summary. Each maps to a different affordance — Behind gets a Pull
// button, Diverged gets Resolve, Unconfigured gets a setup link.
type Reason int

const (
	ReasonSynced Reason = iota
	ReasonUnconfigured
	ReasonNeverFetched
	ReasonUncommitted
	ReasonAhead
	ReasonBehind
	ReasonDiverged
	ReasonConflict
	ReasonMerging
	// ReasonLastRunFailed — the most recent journalled operation failed. This
	// is the state that used to have no indicator at all: the Stop hook's sync
	// refused for days and every surface still read "synced".
	ReasonLastRunFailed
	// ReasonLastRunRefused — the tripwire stopped a push because something
	// looked like a credential. Separate from Failed because it is the safety
	// property working, and it needs its own row and its own wording.
	ReasonLastRunRefused
)

// Report is the whole ambient readout.
type Report struct {
	Level   Level
	Reason  Reason
	Summary string // one line, safe as a tray tooltip
	Ahead   int
	Behind  int
	// Action is the command that clears this state, or "" when there is
	// nothing to run (healthy, or a state that needs a human).
	Action string
}

// tooltipMax is Windows' Shell_NotifyIcon tooltip cap in UTF-16 code units.
// Summary is written to stay well under it; Tooltip enforces it.
const tooltipMax = 127

// Of derives the ambient state from a status snapshot and the most recent
// journal record (zero value when nothing has been recorded). It is pure — all
// the I/O already happened in status.Gather and journal.Read — so the UI can
// call it on every poll and tests can drive every branch from a literal.
//
// The order is the priority order: the worst true thing wins the tray, because
// the tray has exactly one colour to spend.
func Of(info status.Info, last journal.Record) Report {
	d := info.Divergence
	r := Report{Ahead: d.Ahead, Behind: d.Behind}

	switch {
	case !info.HasStaging:
		r.Level, r.Reason = Amber, ReasonUnconfigured
		r.Summary = "Not set up yet — no staging repo"
		r.Action = "clauderig init"

	case d.Merging:
		// A conflicted pull left the repo half-merged. Every later sync fails
		// against this until someone finishes it, so it outranks the counts.
		r.Level, r.Reason = Red, ReasonMerging
		r.Summary = "Unresolved merge in staging — finish or abort it"

	case d.Diverged() && d.Conflict:
		r.Level, r.Reason = Red, ReasonConflict
		r.Summary = fmt.Sprintf("Diverged — %s, %s; merging will conflict",
			commits(d.Ahead, "ahead"), commits(d.Behind, "behind"))

	case d.Diverged():
		// Still red: a ff-only pull cannot resolve this, so it will sit here
		// indefinitely — which is exactly how the incident stayed invisible.
		r.Level, r.Reason = Red, ReasonDiverged
		r.Summary = fmt.Sprintf("Diverged — %s, %s; merges cleanly",
			commits(d.Ahead, "ahead"), commits(d.Behind, "behind"))

	case last.Outcome == journal.OutcomeRefused:
		// Nothing was pushed, so divergence counts look healthy and every
		// other check below would report green. Without this branch the
		// tripwire is invisible again — which is the whole reason the journal
		// exists.
		r.Level, r.Reason = Red, ReasonLastRunRefused
		r.Summary = fmt.Sprintf("Sync refused — %s look like credentials", values(len(last.Leaks)))

	case last.Outcome == journal.OutcomeFailed:
		r.Level, r.Reason = Red, ReasonLastRunFailed
		r.Summary = "Last " + string(last.Op) + " failed"
		if last.Error != "" {
			r.Summary += ": " + last.Error
		}

	case !d.Tracked:
		r.Level, r.Reason = Amber, ReasonNeverFetched
		r.Summary = "Never fetched " + d.Ref
		r.Action = "clauderig pull"

	case d.Behind > 0:
		r.Level, r.Reason = Amber, ReasonBehind
		r.Summary = commits(d.Behind, "behind") + " the remote"
		r.Action = "clauderig pull"

	case d.Ahead > 0:
		r.Level, r.Reason = Amber, ReasonAhead
		r.Summary = commits(d.Ahead, "ahead") + " — not pushed yet"
		r.Action = "clauderig sync"

	case info.Dirty:
		// Level with the remote but the staging tree has loose changes: a sync
		// started and did not finish.
		r.Level, r.Reason = Amber, ReasonUncommitted
		r.Summary = "Staging has uncommitted changes"
		r.Action = "clauderig sync"

	default:
		r.Level, r.Reason = Green, ReasonSynced
		r.Summary = "Up to date"
	}
	return r
}

// Tooltip renders the tray tooltip: the machine name plus the summary, clamped
// to what Windows accepts.
func (r Report) Tooltip(machine string) string {
	s := r.Summary
	if machine != "" {
		s = machine + " — " + s
	}
	return truncate(s, tooltipMax)
}

// truncate clamps to n UTF-16 code units, since that is the unit Windows counts.
func truncate(s string, n int) string {
	units, runes := 0, []rune(s)
	for i, c := range runes {
		w := 1
		if c > 0xFFFF { // encodes as a surrogate pair
			w = 2
		}
		if units+w > n {
			return string(runes[:i])
		}
		units += w
	}
	return s
}

// values renders "1 value" / "12 values".
func values(n int) string {
	if n == 1 {
		return "1 value"
	}
	return fmt.Sprintf("%d values", n)
}

// commits renders "1 commit behind" / "65 commits behind".
func commits(n int, direction string) string {
	if n == 1 {
		return "1 commit " + direction
	}
	return fmt.Sprintf("%d commits %s", n, direction)
}

// String returns the stable lowercase token for a Reason — what the window's
// CSS branches on and what `status --json` emits. Explicit rather than derived,
// so adding a Reason is a compile-time prompt to decide what it's called
// everywhere at once.
func (r Reason) String() string {
	switch r {
	case ReasonSynced:
		return "synced"
	case ReasonUnconfigured:
		return "unconfigured"
	case ReasonNeverFetched:
		return "never-fetched"
	case ReasonUncommitted:
		return "uncommitted"
	case ReasonAhead:
		return "ahead"
	case ReasonBehind:
		return "behind"
	case ReasonDiverged:
		return "diverged"
	case ReasonConflict:
		return "conflict"
	case ReasonMerging:
		return "merging"
	case ReasonLastRunFailed:
		return "last-run-failed"
	case ReasonLastRunRefused:
		return "last-run-refused"
	default:
		return "unknown"
	}
}
